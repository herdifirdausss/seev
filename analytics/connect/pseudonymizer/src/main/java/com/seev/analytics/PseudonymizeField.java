package com.seev.analytics;

import org.apache.kafka.common.config.ConfigDef;
import org.apache.kafka.connect.connector.ConnectRecord;
import org.apache.kafka.connect.transforms.Transformation;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

/**
 * Kafka Connect SMT that removes approved identity fields and replaces them
 * with deterministic HMAC-SHA-256 pseudonyms. The connector uses JSON without
 * schemas after Debezium's unwrap transform, so this transform intentionally
 * handles Map values recursively and never logs field values.
 */
public class PseudonymizeField<R extends ConnectRecord<R>> implements Transformation<R> {
    private static final String FIELDS = "fields";
    private static final String SALT_FILE = "salt.file";

    private List<String> fields = Collections.emptyList();
    private byte[] salt;

    @Override
    public R apply(R record) {
        if (record == null || record.value() == null) {
            return record;
        }
        Object value = rewrite(record.value());
        return record.newRecord(
                record.topic(),
                record.kafkaPartition(),
                record.keySchema(),
                record.key(),
                record.valueSchema(),
                value,
                record.timestamp(),
                record.headers());
    }

    @Override
    public ConfigDef config() {
        return new ConfigDef()
                .define(FIELDS, ConfigDef.Type.LIST, ConfigDef.Importance.HIGH,
                        "Source field names that must be pseudonymized")
                .define(SALT_FILE, ConfigDef.Type.STRING, ConfigDef.Importance.HIGH,
                        "Absolute path to a local-only HMAC salt file");
    }

    @Override
    public void configure(Map<String, ?> configs) {
        Object rawFields = configs.get(FIELDS);
        if (rawFields instanceof List<?> list) {
            fields = list.stream().map(Object::toString).map(this::normalise).toList();
        } else {
            fields = parseFields(Objects.toString(rawFields, ""));
        }
        String saltFile = Objects.toString(configs.get(SALT_FILE), "").trim();
        if (saltFile.isEmpty()) {
            throw new IllegalArgumentException("pseudonymizer salt.file is required");
        }
        try {
            salt = Files.readAllBytes(Path.of(saltFile));
        } catch (Exception err) {
            throw new IllegalArgumentException("cannot read pseudonymizer salt file", err);
        }
        if (salt.length == 0) {
            throw new IllegalArgumentException("pseudonymizer salt file is empty");
        }
    }

    @Override
    public void close() {
        if (salt != null) {
            java.util.Arrays.fill(salt, (byte) 0);
            salt = null;
        }
        fields = Collections.emptyList();
    }

    private Object rewrite(Object value) {
        if (!(value instanceof Map<?, ?> map)) {
            return value;
        }
        Map<String, Object> output = new LinkedHashMap<>();
        for (Map.Entry<?, ?> entry : map.entrySet()) {
            String name = Objects.toString(entry.getKey(), "");
            Object fieldValue = entry.getValue();
            if (fields.contains(normalise(name))) {
                output.put(outputName(name), fieldValue == null ? null : pseudonym(fieldValue.toString()));
            } else {
                output.put(name, rewrite(fieldValue));
            }
        }
        return output;
    }

    private String pseudonym(String input) {
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(salt, "HmacSHA256"));
            byte[] digest = mac.doFinal(input.getBytes(StandardCharsets.UTF_8));
            return "pseudonym_" + hex(digest);
        } catch (Exception err) {
            throw new IllegalStateException("cannot pseudonymize approved identity field", err);
        }
    }

    private static String outputName(String source) {
        return switch (source.toLowerCase(Locale.ROOT)) {
            case "user_id" -> "user_pseudonym";
            case "customer_id" -> "customer_pseudonym";
            case "merchant_internal_id" -> "merchant_pseudonym";
            default -> source + "_pseudonym";
        };
    }

    private static String normalise(String value) {
        return value.trim().toLowerCase(Locale.ROOT);
    }

    private static List<String> parseFields(String value) {
        List<String> result = new ArrayList<>();
        for (String field : value.split(",")) {
            if (!field.isBlank()) {
                result.add(normalise(field));
            }
        }
        return result;
    }

    private static String hex(byte[] bytes) {
        StringBuilder out = new StringBuilder(bytes.length * 2);
        for (byte value : bytes) {
            out.append(String.format(Locale.ROOT, "%02x", value));
        }
        return out.toString();
    }

    /** Value alias makes the connector configuration explicit and discoverable. */
    public static final class Value<R extends ConnectRecord<R>> extends PseudonymizeField<R> {
    }
}
