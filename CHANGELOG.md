## 1.7.2 / 2025-05-12

* [FEATURE] collect rgw buckets and user quota errors instead of failing

## 1.7.1 / 2024-12-11

* [FIX] use RBD namespace list to get namespaces to iterate for rbd volumes collector.

## 1.7.0 / 2024-12-09

* [FEATURE] **BREAKING CHANGES** Most flags have been replaced by the `config.yaml` (an example can be found [here `config.example.yaml`](/config.example.yaml), Helm chart values have been updated as well [`.config` section](https://github.com/galexrt/extended-ceph-exporter/blob/main/charts/extended-ceph-exporter/values.yaml#L115)).
* [FEATURE] **BREAKING CHANGES** RGW Multi realm is now the default! It can't be disabled, the way to go is to use a `realms.yaml` and/or the appropriate Helm values now.
* [HELM] **BREAKING CHANGES** The RGW options have been moved to the `postInstallJob` section in the chart. Previous RGW options/multi realm config sections are not automatically migrated! You must now use the `.config.rgwRealms` section.

Should there be any issues or questions with these changes, please open an issue.

## 1.6.1 / 2024-12-09

* [CI] crossbuild for amd64 and arm64 platforms via `buildx` - This is a test release to see if it fully works.

## 1.6.0 / 2024-11-11

* [BREAKING] The `rbd_volumes` has been removed till the multi-arch build issues can be addressed.

## 1.5.0 / 2024-10-08

* [FEATURE] WARNING! Currently only the container image has the `rbd_volumes` collector available
* [FEATURE] replace logrus with zap logger

## 1.4.0 / 2024-09-25

* [FEATURE] add tenant name label to the RGW bucket and usage metrics
* [FEATURE] add basic RBD volumes size collector (disabled by default)
* [CHORE] update Golang version to 1.23.x

## 1.3.0 / 2024-07-01

* [FEATURE] add RGW multi realm mode to allow one exporter to scrape multiple RGW realms at the same time
* [FEATURE] add `extraObjects` list for additional resources to the Helm chart

## 1.2.2 / 2024-04-17

* [CHORE] change container image release target

## 1.2.1 / 2024-04-17

* [CHORE] version bump for new release under new namespace

## 1.2.0 / 2024-02-29

* [CHORE] Update ceph-go library to 0.26.0
* [CHORE] Update Prometheus client libraries
* [CHORE] Update Golang version to 1.21.x

## 1.1.0 / 2024-01-02

* [CHORE] Update ceph-go library to 0.25.0
* [CHORE] Update Prometheus client library
* [FEATURE] Add `serviceMonitor.scrapeTimeout` option to Helm chart

## 1.0.3 / 2023-10-18

* [CHORE] Update ceph-go library to 0.24.0
* [FEATURE] helm: add option to use an existing secret for rgw credentials
* [CHORE] Use [helm-docs](https://github.com/norwoodj/helm-docs) to create chart documentation
* [FEATURE] Autodetect the RGW host and autogenerate the RGW access key and secret

## 1.0.2 / 2022-11-14

* [FEATURE] use the dotenv extension to read RGW credentials and endpoint from `.env` file
* [BUGFIX] Increment helm chart version to address documentation changes

## 1.0.1 / 2022-11-14

* [BUGFIX] fix the required flags check to check for the new flag names

## 1.0.0 / 2022-09-26

* [FEATURE] initial release of RGW bucket and user quota metrics module
* [FEATURE] add basic helm chart for deploying the exporter to Kubernetes
