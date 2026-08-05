## Unreleased

* [CHANGE] **BREAKING CHANGES** The metrics namespace changed from `ceph` to `custom` (for example `ceph_rgw_bucket_size` is now `custom_rgw_bucket_size`), to stay compatible with the metric names of the in-house RBD exporter this replaces.
* [CHANGE] **BREAKING CHANGES** The `cache` config block is replaced by `refresh` (see [`config.example.yaml`](/config.example.yaml)). Caching is no longer optional; it is how the background refreshers publish their results.
* [CHANGE] **BREAKING CHANGES** The default enabled collectors are now `rbd_images` and `rbd_image_usage`. The RGW collectors and `rbd_volumes` are still available but disabled by default.
* [CHANGE] **BREAKING CHANGES** The `realm` label on `scrape_collector_duration_seconds` and `scrape_collector_success` is renamed to `client`.
* [CHANGE] The default `timeouts.collector` is raised from `60s` to `3m`, because collecting RBD image usage on a cluster without the `fast-diff` image feature can take minutes.
* [CHANGE] Pools that hold no RBD images are skipped instead of being walked.
* [FEATURE] New `rbd_images` collector exposing per image provisioned size, creation timestamp, tenant ownership from `e2e.*` image metadata, and QoS IOPS limits.
* [FEATURE] New `rbd_image_usage` collector exposing how much space each image actually occupies.
* [FEATURE] Each collector is now refreshed by its own background goroutine on its own interval (`refresh.interval` and `refresh.intervals`), so a slow collector can no longer delay or time out a scrape, and a cheap collector can be kept much fresher than an expensive one.
* [FEATURE] New `custom_rbd_last_refresh_timestamp_seconds{collector}` metric, reporting when each collector last completed a cycle so that a stalled collector is alertable.
* [FEATURE] New `--web.listen-address`, `--collector-timeout`, `--refresh-interval` and `--refresh-intervals` flags. A flag that is explicitly passed now overrides the config file.
* [FEATURE] librados operation timeouts (`rbd.opTimeout`) are applied when librados has none configured, so a request the cluster never answers can no longer block a collector until the exporter is restarted.
* [FIX] The rados connection was assigned to a shadowed variable, so every client was given a nil connection and all RBD collectors failed.
* [FIX] RBD collectors obtained images through `rbd.GetImage`, which returns an unopened handle, so every accessor failed with `ErrImageNotOpen`. Images are now opened read-only and closed again.
* [FIX] RBD collection leaked a rados IO context per pool on every scrape.
* [FIX] Images in a pool's default RBD namespace were skipped, because the namespace list was replaced by the discovered named namespaces and otherwise fell back to the all-namespaces sentinel.
* [FIX] A deployment with only RBD collectors enabled exported no metrics at all, because clients were only created from RGW realms.
* [FIX] The RGW collectors no longer dereference a nil API connection when no realm is configured, and report a clear error instead.
* [FIX] `realms.yaml` is now optional, so an RBD-only deployment no longer needs a stub file to start.

## 1.8.0 / 2025-11-18

* [FEATURE] Add enabled collectors list to config and Helm chart

## 1.7.3 / 2025-10-01

* [FIX] Install `ca-certificates` package in container image

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
