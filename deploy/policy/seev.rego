package seev

import future.keywords.contains
import future.keywords.if
import future.keywords.in

# DEPLOY-006: Seev-specific deployment policy-as-code.
# Validates rendered Kubernetes manifests against the repository's security and
# operational invariants. Run via: conftest test --policy deploy/policy/ <manifest>

# ---- Deployment security invariants ----

deny contains msg if {
  input.kind == "Deployment"
  container := input.spec.template.spec.containers[_]
  not container.securityContext.allowPrivilegeEscalation == false
  msg := sprintf("Deployment %s/%s: container %q must set allowPrivilegeEscalation=false",
    [input.metadata.namespace, input.metadata.name, container.name])
}

deny contains msg if {
  input.kind == "Deployment"
  container := input.spec.template.spec.containers[_]
  not container.securityContext.readOnlyRootFilesystem == true
  msg := sprintf("Deployment %s/%s: container %q must set readOnlyRootFilesystem=true",
    [input.metadata.namespace, input.metadata.name, container.name])
}

deny contains msg if {
  input.kind == "Deployment"
  not input.spec.template.spec.securityContext.runAsNonRoot == true
  msg := sprintf("Deployment %s/%s: pod securityContext must set runAsNonRoot=true",
    [input.metadata.namespace, input.metadata.name])
}

deny contains msg if {
  input.kind == "Deployment"
  container := input.spec.template.spec.containers[_]
  drops := {cap | cap := container.securityContext.capabilities.drop[_]}
  not "ALL" in drops
  msg := sprintf("Deployment %s/%s: container %q must drop ALL capabilities",
    [input.metadata.namespace, input.metadata.name, container.name])
}

deny contains msg if {
  input.kind == "Deployment"
  input.spec.template.spec.hostNetwork == true
  msg := sprintf("Deployment %s/%s: hostNetwork must not be enabled",
    [input.metadata.namespace, input.metadata.name])
}

deny contains msg if {
  input.kind == "Deployment"
  input.spec.template.spec.hostPID == true
  msg := sprintf("Deployment %s/%s: hostPID must not be enabled",
    [input.metadata.namespace, input.metadata.name])
}

deny contains msg if {
  input.kind == "Deployment"
  input.spec.template.spec.hostIPC == true
  msg := sprintf("Deployment %s/%s: hostIPC must not be enabled",
    [input.metadata.namespace, input.metadata.name])
}

deny contains msg if {
  input.kind == "Deployment"
  not input.spec.template.spec.automountServiceAccountToken == false
  msg := sprintf("Deployment %s/%s: automountServiceAccountToken must be false",
    [input.metadata.namespace, input.metadata.name])
}

# ---- Resource requirements ----

deny contains msg if {
  input.kind == "Deployment"
  container := input.spec.template.spec.containers[_]
  not container.resources.requests
  msg := sprintf("Deployment %s/%s: container %q must define resource requests",
    [input.metadata.namespace, input.metadata.name, container.name])
}

deny contains msg if {
  input.kind == "Deployment"
  container := input.spec.template.spec.containers[_]
  not container.resources.limits
  msg := sprintf("Deployment %s/%s: container %q must define resource limits",
    [input.metadata.namespace, input.metadata.name, container.name])
}

# ---- Probes required on main containers ----

deny contains msg if {
  input.kind == "Deployment"
  container := input.spec.template.spec.containers[_]
  not container.name in ["wait-for-data"]
  not container.readinessProbe
  msg := sprintf("Deployment %s/%s: container %q must define a readinessProbe",
    [input.metadata.namespace, input.metadata.name, container.name])
}

deny contains msg if {
  input.kind == "Deployment"
  container := input.spec.template.spec.containers[_]
  not container.name in ["wait-for-data"]
  not container.livenessProbe
  msg := sprintf("Deployment %s/%s: container %q must define a livenessProbe",
    [input.metadata.namespace, input.metadata.name, container.name])
}

# ---- Rollout strategy must be explicit ----

deny contains msg if {
  input.kind == "Deployment"
  not input.spec.strategy.type
  msg := sprintf("Deployment %s/%s: rollout strategy.type must be explicitly set",
    [input.metadata.namespace, input.metadata.name])
}

# ---- Placeholder values must not reach staging ----

placeholder_pattern := `REPLACE_WITH_|UNKNOWN`

deny contains msg if {
  input.kind == "Deployment"
  input.metadata.labels["seev.io/environment"] == "staging"
  container := input.spec.template.spec.containers[_]
  env := container.env[_]
  regex.match(placeholder_pattern, env.value)
  msg := sprintf("Deployment %s/%s: container %q env %q contains a placeholder value",
    [input.metadata.namespace, input.metadata.name, container.name, env.name])
}

# ---- Seev service label required ----

deny contains msg if {
  input.kind == "Deployment"
  namespace := input.metadata.namespace
  namespace in ["seev-app"]
  not input.metadata.labels["seev.io/service"]
  msg := sprintf("Deployment %s/%s: seev.io/service label is required in namespace %s",
    [namespace, input.metadata.name, namespace])
}

# ---- NetworkPolicy: default-deny must exist in seev namespaces ----

warn contains msg if {
  input.kind == "NetworkPolicy"
  input.metadata.name == "default-deny"
  msg := sprintf("NetworkPolicy default-deny present in namespace %s", [input.metadata.namespace])
}

# ---- Argo CD Application: must not use default project for cloud environments ----

deny contains msg if {
  input.kind == "Application"
  input.spec.project == "default"
  label := input.metadata.labels["seev.io/environment"]
  label != "local"
  msg := sprintf("Application %s must not use project=default for environment %q (use project=seev)",
    [input.metadata.name, label])
}

# ---- Argo CD Application: only local may use mutable main revision ----

deny contains msg if {
  input.kind == "Application"
  input.spec.source.targetRevision == "main"
  label := input.metadata.labels["seev.io/environment"]
  label != "local"
  msg := sprintf("Application %s: targetRevision=main is not allowed for environment %q; pin to a commit SHA or release tag",
    [input.metadata.name, label])
}
