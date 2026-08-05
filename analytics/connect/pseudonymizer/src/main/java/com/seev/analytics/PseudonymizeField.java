package com.seev.analytics;

import org.apache.kafka.common.config.ConfigDef;
import org.apache.kafka.connect.connector.ConnectRecord;
import org.apache.kafka.connect.data.Field;
import org.apache.kafka.connect.data.Schema;
import org.apache.kafka.connect.data.SchemaBuilder;
import org.apache.kafka.connect.data.Struct;
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
 * with deterministic HMAC-SHA-256 pseudonyms. This runs in the SMT chain
 * after Debezium's unwrap transform, where a record's value is still a typed
 * Connect Struct/Schema pair (schemas.enable=false only affects the final
 * JSON serialization, not the internal SMT pipeline) — so this transform
 * rebuilds both the Struct and its Schema, rather than treating the value as
 * a plain Map. Schemaless Map values are also supported, for configurations
 * that disable Struct-based conversion upstream. This transform never logs
 * field values.
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
        Object originalValue = record.value();
        Schema originalSchema = record.valueSchema();
        Object newValue;
        Schema newSchema;
        if (originalValue instanceof Struct struct) {
            newSchema = rewriteSchema(struct.schema());
            newValue = rewriteStruct(struct, newSchema);
        } else if (originalValue instanceof Map<?, ?> map) {
            newSchema = originalSchema;
            newValue = rewriteMap(map);
        } else {
            return record;
        }
        return record.newRecord(
                record.topic(),
                record.kafkaPartition(),
                record.keySchema(),
                record.key(),
                newSchema,
                newValue,
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
            fields = list.stream().map(Object::toString).map(PseudonymizeField::normalise).toList();
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

    private Schema rewriteSchema(Schema originalSchema) {
        SchemaBuilder builder = SchemaBuilder.struct().name(originalSchema.name()).version(originalSchema.version());
        if (originalSchema.isOptional()) {
            builder.optional();
        }
        for (Field field : originalSchema.fields()) {
            if (fields.contains(normalise(field.name()))) {
                builder.field(outputName(field.name()), Schema.OPTIONAL_STRING_SCHEMA);
            } else {
                builder.field(field.name(), field.schema());
            }
        }
        return builder.build();
    }

    private Struct rewriteStruct(Struct original, Schema newSchema) {
        Struct output = new Struct(newSchema);
        for (Field field : original.schema().fields()) {
            Object fieldValue = original.get(field);
            if (fields.contains(normalise(field.name()))) {
                output.put(outputName(field.name()), fieldValue == null ? null : pseudonym(fieldValue.toString()));
            } else {
                output.put(field.name(), fieldValue);
            }
        }
        return output;
    }

    private Object rewriteMap(Map<?, ?> map) {
        Map<String, Object> output = new LinkedHashMap<>();
        for (Map.Entry<?, ?> entry : map.entrySet()) {
            String name = Objects.toString(entry.getKey(), "");
            Object fieldValue = entry.getValue();
            if (fields.contains(normalise(name))) {
                output.put(outputName(name), fieldValue == null ? null : pseudonym(fieldValue.toString()));
            } else if (fieldValue instanceof Map<?, ?> nested) {
                output.put(name, rewriteMap(nested));
            } else {
                output.put(name, fieldValue);
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
