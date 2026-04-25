# Rancher Fleet layout

This directory contains a Fleet-ready GitOps layout for Rancher-managed clusters.

- `fookie/fleet.yaml`: bundle definition and target customizations
- `fookie/values-common.yaml`: shared defaults
- `fookie/values-dev.yaml`: dev overrides
- `fookie/values-staging.yaml`: staging overrides
- `fookie/values-prod.yaml`: prod overrides

Cluster labels expected by `fleet.yaml`:

- `env=dev`
- `env=staging`
- `env=prod`
