# extended-ceph-exporter

A Prometheus exporter to provide "extended" metrics about a Ceph cluster's running components (e.g., RGW).

Due to the closure of Koor Technologies, Inc. this repository has been made to continue the work on the extended-ceph-exporter project.

[![Ceph - RGW Bucket Usage Overview Grafana Dashboard Screenshot](grafana/ceph-rgw-bucket-usage-overview.png)](grafana/)

## Requirements

* Needs a Ceph cluster up and running (Rook Ceph clusters with CephObjectStores work as well, checkout the [Rook section](#rook)).

* Needs a RGW user with admin or the following "caps": `buckets=read;users=read;usage=read;metadata=read;zone=read`

    ```
    radosgw-admin user create --uid extended-ceph-exporter --display-name "extended-ceph-exporter admin user" --caps "buckets=read;users=read;usage=read;metadata=read;zone=read"
    # Access key / "Username"
    radosgw-admin user info --uid extended-ceph-exporter | jq '.keys[0].access_key'
    # Secret key / "Password
    radosgw-admin user info --uid extended-ceph-exporter | jq '.keys[0].secret_key'
    ```

## Rook

If using Rook to manage RGWs, the admin user may also be created using a `CephOjectStoreUser` resource:

```yaml
apiVersion: ceph.rook.io/v1
kind: CephObjectStoreUser
metadata:
  name: extended-ceph-exporter
  namespace: rook-ceph
spec:
  store: <objectstore-name>
  clusterNamespace: rook-ceph
  displayName: extended-ceph-exporter
  capabilities:
    buckets: read
    users: read
    usage: read
    metadata: read
    zone: read
```

Applying this will create an user with all permissions needed.

## Quickstart

* Clone the repository, download release binary or pull the container image:
  ```console
  git clone https://github.com/galexrt/extended-ceph-exporter
  cd extended-ceph-exporter
  ```

* Create a copy of the `config.example.yaml` and `realms.example.yaml` files, and rename the files to remove the `.example` from the names.
    * Make sure to configure your RGW admin user credentials and endpoint in the `realms.yaml` file.

* Configure Prometheus to collect metrics from the exporter from `:9138/metrics` endpoint using a static configuration, here's a sample scrape job from the `prometheus.yml`:

  ```yaml
  # For more information on Prometheus scrape_configs:
  # https://prometheus.io/docs/prometheus/latest/configuration/configuration/#scrape_config
  scrape_configs:

    - job_name: "extended-ceph-metrics"

      # Override the global default and scrape targets from this job every 30 seconds.
      scrape_interval: 30s

      static_configs:
        # Please change the ip address `127.0.0.1` to target the server the exporter is running on
        - targets: ['127.0.0.1:9138']
  ```

* To run the exporter locally you can use one of the methods:
    * Using `go` command, run `go run .`
    * Download a [release binary](releases).
    * Use the container image avaialble from [ghcr.io/galexrt/extended-ceph-exporter](https://github.com/galexrt/extended-ceph-exporter/pkgs/container/extended-ceph-exporter).
    * [Helm chart](charts/extended-ceph-exporter/README.md) for Kubernetes/OpenShift deployment.

* Should you have Grafana running for metrics visulization, check out the available [Grafana dashboards](grafana/).

### Helm

To install the exporter to Kubernetes using Helm, please check out the [extended-ceph-exporter Helm Chart README.md file](charts/extended-ceph-exporter/README.md).

## Collectors

There is varying support for collectors. The tables
below list all existing collectors and the required Ceph components.

### Enabled by default

| Name              |                                        Description                                        | Ceph Component |
| :---------------- | :---------------------------------------------------------------------------------------: | -------------- |
| `rbd_images`      | Exposes RBD image provisioned size, creation time, tenant ownership and QoS IOPS limits.  | RBD            |
| `rbd_image_usage` |            Exposes how much space each RBD image actually occupies. Expensive.            | RBD            |

### Disabled by default

| Name             |                                    Description                                     | Ceph Component |
| :--------------- | :--------------------------------------------------------------------------------: | -------------- |
| `rgw_buckets`    |          Exposes RGW Bucket Usage and Quota metrics from the Ceph cluster.          | RGW            |
| `rgw_user_quota` |                Exposes RGW User Quota metrics from the Ceph cluster.                | RGW            |
| `rbd_volumes`    | Exposes RBD volumes size. Superseded by `rbd_images`, which also carries ownership. | RBD            |

## RBD: Image Ownership

`rbd_images` reads tenant ownership from RBD image metadata, exposing it on
`custom_rbd_image_owner` (whose value is always `1`) as labels:

| Image metadata key        | Label                 |
| :------------------------ | :-------------------- |
| `e2e.resource_type`       | `resource_type`       |
| `e2e.vm_id`               | `vm_id`               |
| `e2e.project`             | `project`             |
| `e2e.customer_id`         | `customer_id`         |
| `e2e.customer_email`      | `customer_email`      |
| `e2e.billed_customer_crn` | `billed_customer_crn` |

Note that `customer_id` is the owning account while `billed_customer_crn` is the
billed entity, and the two are not necessarily the same. Group by
`billed_customer_crn` for billing and by `customer_id` for attribution.

An image carrying none of these keys produces no `owner` series at all, so
untagged images can be counted by comparing against `custom_rbd_image_provisioned_bytes`.
An image carrying only some of them is still reported, with the missing labels
left empty, so that it stays visible instead of dropping out of tenant queries.

QoS limits are read from the `conf_rbd_qos_read_iops_limit` and
`conf_rbd_qos_write_iops_limit` metadata keys. These are Ceph per image config
overrides rather than annotations, so they are live enforcement values. A metric
is only emitted for images that actually carry the override, because Ceph reads
an explicitly configured `0` as *unlimited*, which is a different state from no
limit being configured at all.

## Background Refresh

Every enabled collector is refreshed by its own background goroutine on its own
interval, and a scrape only ever replays the last completed refresh. Scrapes are
therefore always fast, however expensive the underlying collection is.

This matters because `rbd_image_usage` has to visit every backing object unless
the `fast-diff` image feature is enabled, which can take far longer than a
Prometheus scrape timeout. Give it a longer interval than `rbd_images`, which
only reads the image header and its metadata:

```yaml
refresh:
  interval: "4m"
  intervals:
    rbd_images: "60s"
    rbd_image_usage: "4m"
```

`custom_rbd_last_refresh_timestamp_seconds{collector="..."}` reports when each
collector last completed, and is `0` until its first cycle finishes. Alert on it
to catch a collector that has stopped making progress:

```promql
time() - custom_rbd_last_refresh_timestamp_seconds > 600
```

Be aware that collectors refreshed at different intervals are observed at
different points in time. Joining across them, for example
`custom_rbd_image_used_bytes` against `custom_rbd_image_owner`, can therefore
briefly mismatch while an image is being created or removed. Use the same
interval for both if such a join has to be exact.

## RGW: Multiple Realms

You can use the exporter to scrape metrics from multiple RGW realms by providing multiple RGWs in the realm config file.

An example realm config file can be found here [`realms.example.yaml`](realms.example.yaml).

## Flags

```console
$ extended-ceph-exporter --help
Usage of exporter:
      --collectors-enabled strings           List of enabled collectors (please refer to the readme for a list of all available collectors) (default [rgw_user_quota,rgw_buckets])
      --config config.yaml                   Config file path (default name config.yaml , current and `/config` directory).
      --realms-config --multi-realm-config   Path to your realms.yaml config file (old flag name: --multi-realm-config) (default "realms.yaml")
      --version                              Show version info and exit
pflag: help requested
exit status 2
```

## Development

### Requirements

* Golang 1.23.x (or higher should work)
* Ceph development files/libraries (`librados`, `librdb`)
    * If you are using `nix`, the `flake.nix` should be satisfy these lib dependencies.
* `helm`

### Making Changes to the Helm Chart

When changing anything in the Helm Chart, the version in the `Chart.yaml` needs to be increased according to [Semver](https://semver.org/).
Additionally `make helm-doc` must be run afterwards and the changes to the Helm Chart's `README.md` must be commited as well.

### Debugging

A VSCode debug config is available to run and debug the project.

To make the exporter talk with a Ceph RGW S3 endpoint, create a copy of the `config.example.yaml` and `realms.example.yaml` files, and rename the files to remove the `.example` from the names.
Be sure ot add your Ceph RGW S3 endpoint and credentials in it.
