/*
Copyright 2022 Koor Technologies, Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package collector

import (
	"context"
	"fmt"
	"slices"

	"github.com/ceph/go-ceph/rados"
	"github.com/ceph/go-ceph/rbd"
	"github.com/galexrt/extended-ceph-exporter/pkg/config"
	"go.uber.org/multierr"
)

const (
	// defaultRBDNamespace is the unnamed namespace every pool has. It is not
	// reported by rbd.NamespaceList, so it always has to be walked explicitly.
	defaultRBDNamespace = ""

	// rbdDirectoryObject is the RADOS object librbd keeps its image index in.
	// Its presence is what marks a pool as holding RBD images.
	rbdDirectoryObject = "rbd_directory"
)

// RBDImageFunc is called once per RBD image. The image is already open for
// reading and gets closed again once the function returns.
type RBDImageFunc func(pool, namespace, name string, image *rbd.Image) error

// eachRBDImage walks every RBD pool and namespace and hands each image to fn.
//
// Errors are accumulated rather than returned immediately, so that a single
// unreadable pool or image cannot hide the metrics of all the others.
func eachRBDImage(ctx context.Context, client *Client, fn RBDImageFunc) error {
	if client.Rados == nil {
		return fmt.Errorf("client %q has no rados connection, RBD collectors need a ceph config with authentication info (see rbd.cephConfig)", client.Name)
	}

	pools, err := client.Rados.ListPools()
	if err != nil {
		return fmt.Errorf("failed to list pools. %w", err)
	}

	if len(client.Config.RBD.Pools) > 0 {
		// Remove any pools not in our list
		pools = slices.DeleteFunc(pools, func(pool string) bool {
			return !slices.ContainsFunc(client.Config.RBD.Pools, func(rp *config.RBDPool) bool {
				return rp.Name == pool
			})
		})
	}

	var errs error
	for _, pool := range pools {
		if err := ctx.Err(); err != nil {
			return multierr.Append(errs, err)
		}

		ioctx, err := client.Rados.OpenIOContext(pool)
		if err != nil {
			errs = multierr.Append(errs, fmt.Errorf("failed to open rados IO context for %s pool. %w", pool, err))
			continue
		}

		if isRBDPool(ioctx) {
			errs = multierr.Append(errs, eachRBDImageInPool(ctx, client, ioctx, pool, fn))
		}

		// Every IO context has to be released again, otherwise each collection
		// leaks librados state for the lifetime of the process.
		ioctx.Destroy()
	}

	return errs
}

// isRBDPool reports whether a pool holds RBD images, so that pools belonging to
// other Ceph components (.mgr, CephFS, RGW) are skipped instead of walked.
//
// An RBD enabled pool that is still empty has no image index yet and is skipped
// as well, which is harmless because it has no images to report.
func isRBDPool(ioctx *rados.IOContext) bool {
	ioctx.SetNamespace(defaultRBDNamespace)
	if _, err := ioctx.Stat(rbdDirectoryObject); err == nil {
		return true
	}

	// A pool may keep all of its images inside named namespaces, in which case
	// the default namespace holds no image index.
	namespaces, err := rbd.NamespaceList(ioctx)

	return err == nil && len(namespaces) > 0
}

// namespacesForPool returns the namespaces to walk for a pool. Explicit
// configuration wins, otherwise the default namespace plus every namespace the
// pool reports is used.
//
// rbd.NamespaceList only reports named namespaces, so the default one has to be
// added explicitly. Replacing the list with just the discovered namespaces
// would silently hide every image that lives outside a named namespace.
func namespacesForPool(client *Client, ioctx *rados.IOContext, pool string) ([]string, error) {
	if idx := slices.IndexFunc(client.Config.RBD.Pools, func(rp *config.RBDPool) bool {
		return rp.Name == pool
	}); idx > -1 && len(client.Config.RBD.Pools[idx].Namespaces) > 0 {
		return client.Config.RBD.Pools[idx].Namespaces, nil
	}

	found, err := rbd.NamespaceList(ioctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces for %s pool. %w", pool, err)
	}

	return append([]string{defaultRBDNamespace}, found...), nil
}

func eachRBDImageInPool(ctx context.Context, client *Client, ioctx *rados.IOContext, pool string, fn RBDImageFunc) error {
	namespaces, err := namespacesForPool(client, ioctx, pool)
	if err != nil {
		return err
	}

	var errs error
	for _, namespace := range namespaces {
		ioctx.SetNamespace(namespace)

		names, err := rbd.GetImageNames(ioctx)
		if err != nil {
			errs = multierr.Append(errs, fmt.Errorf("failed to get image names from %s pool (namespace: %q). %w", pool, namespace, err))
			continue
		}

		for _, name := range names {
			// A librados call that is already blocked cannot be interrupted,
			// but stopping between images keeps a cancelled or timed out
			// collection from walking the rest of the cluster.
			if err := ctx.Err(); err != nil {
				return multierr.Append(errs, err)
			}

			errs = multierr.Append(errs, withRBDImage(ioctx, pool, namespace, name, fn))
		}
	}

	return errs
}

// withRBDImage opens one image read-only, hands it to fn and always closes it
// again. rbd.GetImage on its own returns an unopened handle, which makes every
// accessor fail with ErrImageNotOpen.
func withRBDImage(ioctx *rados.IOContext, pool, namespace, name string, fn RBDImageFunc) error {
	image, err := rbd.OpenImageReadOnly(ioctx, name, rbd.NoSnapshot)
	if err != nil {
		return fmt.Errorf("failed to open image %s/%s (namespace: %q). %w", pool, name, namespace, err)
	}

	err = fn(pool, namespace, name, image)

	if cerr := image.Close(); cerr != nil {
		err = multierr.Append(err, fmt.Errorf("failed to close image %s/%s (namespace: %q). %w", pool, name, namespace, cerr))
	}

	return err
}
