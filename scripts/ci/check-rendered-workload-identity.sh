#!/usr/bin/env bash
# Validate the provider identity annotation on the rendered Vendor
# ServiceAccount.  This deliberately consumes rendered YAML rather than Helm
# values so a chart wiring regression is observable at the workload boundary.
set -euo pipefail

if [[ $# -ne 2 ]]; then
	echo "Usage: $0 <rendered-manifest.yaml> <aws|gcp|local>" >&2
	exit 2
fi

manifest=$1
provider=$2
[[ -s "$manifest" ]] || {
	echo "identity-check: manifest is missing: $manifest" >&2
	exit 1
}

case "$provider" in
	aws) annotation='eks.amazonaws.com/role-arn:' ;;
	gcp) annotation='iam.gke.io/gcp-service-account:' ;;
	local) annotation='' ;;
	*)
		echo "identity-check: provider must be aws, gcp, or local" >&2
		exit 2
		;;
esac

if [[ "$provider" == local ]]; then
	if grep -Fq 'eks.amazonaws.com/role-arn:' "$manifest" ||
		grep -Fq 'iam.gke.io/gcp-service-account:' "$manifest"; then
		echo "identity-check: local manifest unexpectedly contains a cloud identity annotation" >&2
		exit 1
	fi
	printf 'identity-check: local manifest has no cloud identity annotation\n'
	exit 0
fi

# ServiceAccount documents are separated by `---`.  Keep the check narrow to
# the Vendor ServiceAccount; annotations on a Deployment or another account do
# not satisfy the workload-identity contract.
if awk -v wanted='name: seev-vendor' -v annotation="$annotation" '
	BEGIN { in_doc=0; has_name=0; has_annotation=0; found=0 }
	/^kind: ServiceAccount[[:space:]]*$/ {
		in_doc=1; has_name=0; has_annotation=0; next
	}
	/^---[[:space:]]*$/ {
		if (in_doc && has_name && has_annotation) found=1
		in_doc=0; has_name=0; has_annotation=0; next
	}
	in_doc && index($0, wanted) { has_name=1 }
	in_doc && index($0, annotation) { has_annotation=1 }
	END {
		if (in_doc && has_name && has_annotation) found=1
		exit(found ? 0 : 1)
	}
' "$manifest"; then
	printf 'identity-check: Vendor ServiceAccount carries %s\n' "$annotation"
else
	echo "identity-check: Vendor ServiceAccount is missing $annotation" >&2
	exit 1
fi
