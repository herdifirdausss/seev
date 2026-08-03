#!/usr/bin/env bash
# Validate the K0 inventory contract without starting or mutating a runtime.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INVENTORY_DIR="$ROOT_DIR/deploy/inventory"
EVIDENCE_DIR="$ROOT_DIR/docs/evidence/k0"

for command in ruby git docker rg; do
  command -v "$command" >/dev/null || {
    echo "deployment-inventory-check: $command is required" >&2
    exit 2
  }
done

ruby - "$ROOT_DIR" <<'RUBY'
require "digest"
require "fileutils"
require "set"
require "shellwords"
require "yaml"

root = ARGV.fetch(0)
inventory = File.join(root, "deploy", "inventory")
evidence = File.join(root, "docs", "evidence", "k0")
required = %w[
  services.yaml ports.yaml dependencies.yaml routes.yaml data-stores.yaml
  messaging.yaml jobs.yaml configuration.yaml secrets.yaml vendor-network.yaml
  first-deployment-scope.yaml resource-baseline.yaml
]
load_inventory = lambda do |file|
  path = File.join(inventory, file)
  abort "missing inventory: #{path}" unless File.file?(path)
  value = YAML.load_file(path)
  abort "empty inventory: #{file}" unless value.is_a?(Hash)
  value
end
required.each { |file| load_inventory.call(file) }

services = load_inventory.call("services.yaml")
records = Array(services["services"])
canonical = %w[gateway auth ledger payin payout fraud admin-bff assurance vendor]
names = records.map { |record| record.fetch("name") }
abort "canonical service set mismatch: #{names.inspect}" unless names.sort == canonical.sort && names.uniq.length == canonical.length
records.each do |record|
  %w[name current roles ownership exposure first_deployment sources].each { |key| abort "service missing #{key}: #{record.inspect}" unless record.key?(key) }
  %w[command compose_service build_service image binary app_name].each { |key| abort "current service field missing #{key}: #{record.inspect}" unless record.dig("current", key) }
  abort "service has no first-deployment decision: #{record["name"]}" unless [true, false].include?(record.dig("first_deployment", "enabled"))
end

compose_services = `cd #{Shellwords.escape(root)} && docker compose --profile app config --services 2>/dev/null`.lines.map(&:strip).reject(&:empty?)
expected_compose = records.map { |record| record.dig("current", "compose_service") } +
  Array(services["auxiliary_processes"]).select { |record| record["compose_service"] }.map { |record| record["compose_service"] }
abort "Compose services missing from inventory: #{(compose_services - expected_compose).join(", ")}" unless (compose_services - expected_compose).empty?
abort "Inventory services missing from Compose: #{(expected_compose - compose_services).join(", ")}" unless (expected_compose - compose_services).empty?

ports = Array(load_inventory.call("ports.yaml")["listeners"])
abort "ports.yaml has no listeners" if ports.empty?
valid_owners = canonical + %w[postgres redis rabbitmq squid edge]
keys = Set.new
ports.each do |record|
  %w[name service port protocol transport bind exposure_class downstream_owner sources].each { |key| abort "port missing #{key}: #{record.inspect}" unless record.key?(key) }
  abort "invalid port owner: #{record["service"]}" unless valid_owners.include?(record["service"])
  abort "port has no downstream owner: #{record["name"]}" if record["downstream_owner"].to_s.empty?
  key = [record["service"], record["port"], record["protocol"], record["transport"]]
  abort "duplicate listener: #{key.inspect}" unless keys.add?(key)
end

dependencies = Array(load_inventory.call("dependencies.yaml")["calls"])
abort "dependencies.yaml has no calls" if dependencies.empty?
dependencies.each do |record|
  %w[caller target protocol port readiness_class first_deployment downstream_owner sources].each { |key| abort "dependency missing #{key}: #{record.inspect}" unless record.key?(key) }
end

routes = Array(load_inventory.call("routes.yaml")["routes"])
abort "routes.yaml has no routes" if routes.empty?
routes.each do |record|
  %w[name owner listener path methods auth_class public_exposure first_deployment sources].each { |key| abort "route missing #{key}: #{record.inspect}" unless record.key?(key) }
  if record["name"].to_s.match?(/health|ready|metrics/) && record["public_exposure"] == true
    abort "health/readiness/metrics route is public: #{record["name"]}"
  end
end

stores = load_inventory.call("data-stores.yaml")
databases = Array(stores["postgresql"])
abort "duplicate PostgreSQL owner" unless databases.map { |record| record["owner"] }.uniq.length == databases.length
databases.each do |record|
  %w[name database owner app_user migration_user migrations_path backup_policy sources].each { |key| abort "database missing #{key}: #{record.inspect}" unless record.key?(key) }
end
abort "Redis inventory is empty" if Array(stores["redis"]).empty?
abort "RabbitMQ inventory is empty" if Array(stores["rabbitmq"]).empty?

messaging = load_inventory.call("messaging.yaml")
queues = Array(messaging["queues"])
abort "messaging inventory is empty" if queues.empty?
abort "duplicate queue name" unless queues.map { |queue| queue["name"] }.uniq.length == queues.length
queues.each do |record|
  %w[name exchange routing_keys consumers dead_letter_policy durable sources].each { |key| abort "queue missing #{key}: #{record.inspect}" unless record.key?(key) }
end

jobs = Array(load_inventory.call("jobs.yaml")["jobs"])
abort "jobs inventory is empty" if jobs.empty?
jobs.each do |record|
  %w[name owner trigger first_deployment replica_safety source].each { |key| abort "job missing #{key}: #{record.inspect}" unless record.key?(key) }
  abort "enabled worker contains UNKNOWN_BLOCKER: #{record["name"]}" if record["first_deployment"] == true && record.to_s.include?("UNKNOWN_BLOCKER")
end

configuration = Array(load_inventory.call("configuration.yaml")["keys"])
abort "duplicate configuration key" unless configuration.map { |record| record["name"] }.uniq.length == configuration.length
configuration.each do |record|
  %w[name owner sensitive kubernetes_target required default reloadable source_refs].each { |key| abort "configuration key missing #{key}: #{record.inspect}" unless record.key?(key) }
  abort "sensitive key is not a Secret: #{record["name"]}" if record["sensitive"] == true && record["kubernetes_target"] != "Secret"
end

secrets = Array(load_inventory.call("secrets.yaml")["secrets"])
abort "secret inventory is empty" if secrets.empty?
secrets.each do |record|
  %w[name owner consumers input never_log value_policy source].each { |key| abort "secret missing #{key}: #{record.inspect}" unless record.key?(key) }
  abort "secret may be logged: #{record["name"]}" unless record["never_log"] == true
  abort "secret contains a value: #{record["name"]}" if record.keys.any? { |key| %w[value contents plaintext].include?(key.to_s) }
end

vendors = Array(load_inventory.call("vendor-network.yaml")["vendors"])
abort "vendor inventory is empty" if vendors.empty?
vendors.each do |record|
  %w[name mode first_deployment callbacks outbound proxy_behavior source].each { |key| abort "vendor missing #{key}: #{record.inspect}" unless record.key?(key) }
  %w[proxy_required direct_fallback_allowed].each { |key| abort "vendor proxy field missing #{key}: #{record.inspect}" unless record.dig("proxy_behavior", key) != nil }
end

scope = load_inventory.call("first-deployment-scope.yaml")
%w[enabled_components public_routes private_surfaces feature_flags workers data_policy sources].each { |key| abort "scope missing #{key}" unless scope.key?(key) }
abort "vendor missing from first-deployment scope" unless Array(scope["enabled_components"]).any? { |item| item.to_s.include?("vendor") }
abort "production data policy is not false" unless scope.dig("data_policy", "production_data") == false

resource_status = load_inventory.call("resource-baseline.yaml")["measurement_status"]
abort "resource baseline has no measurement_status" unless resource_status.is_a?(Hash)
%w[R0 R1 R2 R3 R4 R5 R6].each do |profile|
  abort "resource profile #{profile} is not measured or deferred" unless %w[measured deferred].include?(resource_status.dig(profile, "status"))
end

hash_path = File.join(evidence, "generated", "inventory-sha256.txt")
FileUtils.mkdir_p(File.dirname(hash_path))
hashes = required.sort.map do |file|
  path = File.join(inventory, file)
  "#{Digest::SHA256.file(path).hexdigest}  #{path.delete_prefix(root + "/")}\n"
end
File.write(hash_path, hashes.join)
puts "validated #{required.length} machine-readable inventories"
RUBY

if rg -n --glob '*.yaml' --glob '*.md' --glob '*.txt' '(BEGIN (RSA|EC|OPENSSH) PRIVATE KEY|PRIVATE KEY-----|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|password:[[:space:]]+[^<[:space:]][^[:space:]]*)' "$ROOT_DIR/deploy/inventory" "$ROOT_DIR/docs/deployment" "$EVIDENCE_DIR"; then
  echo "deployment-inventory-check: possible secret material found in K0 outputs" >&2
  exit 1
fi

git -C "$ROOT_DIR" diff --check
echo "K0 deployment inventory check passed"
