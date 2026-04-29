# Upgrade Guide

Use this checklist for production upgrades that include CRD, webhook, or chart
changes. Helm installs CRDs on first install, but it does not upgrade or remove
CRDs on `helm upgrade` or `helm uninstall`.

## Preflight

1. Read the release notes for CRD schema, webhook TLS, RBAC, and flag changes.
2. Confirm the target image tag or digest and chart version are from the same
   release.
3. Verify webhook TLS is healthy before changing admission:

   ```bash
   kubectl get validatingwebhookconfiguration
   kubectl get certificate -n fleet-management-system
   kubectl get pods -n fleet-management-system
   ```

4. Export the current release values and CRDs:

   ```bash
   helm get values fleet-management-operator -n fleet-management-system -o yaml > values.backup.yaml
   kubectl get crd \
     pipelines.fleetmanagement.grafana.com \
     pipelinediscoveries.fleetmanagement.grafana.com \
     collectors.fleetmanagement.grafana.com \
     collectordiscoveries.fleetmanagement.grafana.com \
     externalattributesyncs.fleetmanagement.grafana.com \
     remoteattributepolicies.fleetmanagement.grafana.com \
     tenantpolicies.fleetmanagement.grafana.com \
     -o yaml > crds.backup.yaml
   kubectl get pipelines,collectors,collectordiscoveries,pipelinediscoveries,externalattributesyncs,remoteattributepolicies,tenantpolicies -A -o yaml > fleet-crs.backup.yaml
   ```

## Upgrade Order

1. Apply CRD updates before the chart upgrade:

   ```bash
   kubectl apply -f charts/fleet-management-operator/crds/
   ```

2. Upgrade the Helm release:

   ```bash
   helm upgrade fleet-management-operator charts/fleet-management-operator \
     --namespace fleet-management-system \
     -f values-prod.yaml
   ```

3. Wait for the rollout and webhook endpoints:

   ```bash
   kubectl rollout status deploy/fleet-management-operator-controller-manager -n fleet-management-system
   kubectl get endpoints -n fleet-management-system
   ```

4. Create or update a non-production sample CR and verify admission and status
   before changing production objects.

## Rollback Boundaries

`helm rollback` can roll back Deployments, Services, RBAC, and webhook objects
managed by the chart. It cannot safely downgrade CRDs, rewrite stored custom
resources, or undo schema/defaulting changes already accepted by the API server.

For a failed controller rollout with unchanged CRDs:

```bash
helm rollback fleet-management-operator <REVISION> -n fleet-management-system
```

For a failed CRD or webhook rollout:

1. Prefer a forward fix that restores compatibility.
2. If admission is blocking emergency changes, temporarily set the relevant
   webhook `failurePolicy` to `Ignore` or remove the webhook configuration,
   then restore fail-closed admission after the fix.
3. Do not apply older CRD YAML over newer CRDs unless you have confirmed no
   stored objects use fields or versions unknown to the older schema.

## Certificate Rotation

With cert-manager, renew or replace the issuer and wait for the serving
certificate and CA injection to settle before upgrading the manager. With manual
TLS, update the serving Secret and `webhook.caBundle` together so the API server
trusts the new certificate before fail-closed webhooks receive traffic.
