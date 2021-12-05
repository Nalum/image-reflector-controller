/*
Copyright 2020 The Flux authors

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

package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1beta1"
	"github.com/fluxcd/image-reflector-controller/internal/database"
	// +kubebuilder:scaffold:imports
)

func TestImageRepositoryReconciler_canonicalImageName(t *testing.T) {
	g := NewWithT(t)

	// Would be good to test this without needing to do the scanning, since
	// 1. better to not rely on external services being available
	// 2. probably going to want to have several test cases
	repo := imagev1.ImageRepository{
		Spec: imagev1.ImageRepositorySpec{
			Interval: metav1.Duration{Duration: reconciliationInterval},
			Image:    "alpine",
		},
	}
	imageRepoName := types.NamespacedName{
		Name:      "test-canonical-name-" + randStringRunes(5),
		Namespace: "default",
	}

	repo.Name = imageRepoName.Name
	repo.Namespace = imageRepoName.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()

	g.Expect(testEnv.Create(ctx, &repo)).To(Succeed())

	g.Eventually(func() bool {
		err := testEnv.Get(context.Background(), imageRepoName, &repo)
		return err == nil && repo.Status.LastScanResult != nil
	}, timeout).Should(BeTrue())
	g.Expect(repo.Name).To(Equal(imageRepoName.Name))
	g.Expect(repo.Namespace).To(Equal(imageRepoName.Namespace))
	g.Expect(repo.Status.CanonicalImageName).To(Equal("index.docker.io/library/alpine"))
}

func TestImageRepositoryReconciler_fetchImageTags(t *testing.T) {
	g := NewWithT(t)

	registryServer := newRegistryServer()
	defer registryServer.Close()

	versions := []string{"0.1.0", "0.1.1", "0.2.0", "1.0.0", "1.0.1", "1.0.2", "1.1.0-alpha"}
	imgRepo, err := loadImages(registryServer, "test-fetch-"+randStringRunes(5), versions)
	g.Expect(err).ToNot(HaveOccurred())

	repo := imagev1.ImageRepository{
		Spec: imagev1.ImageRepositorySpec{
			Interval: metav1.Duration{Duration: reconciliationInterval},
			Image:    imgRepo,
		},
	}
	objectName := types.NamespacedName{
		Name:      "test-fetch-img-tags-" + randStringRunes(5),
		Namespace: "default",
	}

	repo.Name = objectName.Name
	repo.Namespace = objectName.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()
	g.Expect(testEnv.Create(ctx, &repo)).To(Succeed())

	g.Eventually(func() bool {
		err := testEnv.Get(context.Background(), objectName, &repo)
		return err == nil && repo.Status.LastScanResult != nil
	}, timeout, interval).Should(BeTrue())
	g.Expect(repo.Status.CanonicalImageName).To(Equal(imgRepo))
	g.Expect(repo.Status.LastScanResult.TagCount).To(Equal(len(versions)))
}

func TestImageRepositoryReconciler_repositorySuspended(t *testing.T) {
	g := NewWithT(t)

	repo := imagev1.ImageRepository{
		Spec: imagev1.ImageRepositorySpec{
			Interval: metav1.Duration{Duration: reconciliationInterval},
			Image:    "alpine",
			Suspend:  true,
		},
	}
	imageRepoName := types.NamespacedName{
		Name:      "test-suspended-repo-" + randStringRunes(5),
		Namespace: "default",
	}

	repo.Name = imageRepoName.Name
	repo.Namespace = imageRepoName.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()
	g.Expect(testEnv.Create(ctx, &repo)).To(Succeed())

	r := &ImageRepositoryReconciler{
		Client:   testEnv,
		Scheme:   scheme.Scheme,
		Database: database.NewBadgerDatabase(testBadgerDB),
	}

	key := client.ObjectKeyFromObject(&repo)
	res, err := r.Reconcile(logr.NewContext(ctx, log.NullLogger{}), ctrl.Request{NamespacedName: key})
	g.Expect(err).To(BeNil())
	g.Expect(res.Requeue).ToNot(BeTrue())

	// Make sure no status was written.
	var ir imagev1.ImageRepository
	g.Eventually(func() bool {
		err := testEnv.Get(ctx, imageRepoName, &ir)
		return err == nil
	}, timeout, interval).Should(BeTrue())
	g.Expect(ir.Status.CanonicalImageName).To(Equal(""))
}

func TestImageRepositoryReconciler_reconcileAtAnnotation(t *testing.T) {
	g := NewWithT(t)

	registryServer := newRegistryServer()
	defer registryServer.Close()

	imgRepo, err := loadImages(registryServer, "test-annot-"+randStringRunes(5), []string{"1.0.0"})
	g.Expect(err).ToNot(HaveOccurred())

	repo := imagev1.ImageRepository{
		Spec: imagev1.ImageRepositorySpec{
			Interval: metav1.Duration{Duration: reconciliationInterval},
			Image:    imgRepo,
		},
	}
	objectName := types.NamespacedName{
		Name:      "test-reconcile-at-annot-" + randStringRunes(5),
		Namespace: "default",
	}

	repo.Name = objectName.Name
	repo.Namespace = objectName.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()
	g.Expect(testEnv.Create(ctx, &repo)).To(Succeed())

	g.Eventually(func() bool {
		err := testEnv.Get(ctx, objectName, &repo)
		return err == nil && repo.Status.LastScanResult != nil
	}, timeout, interval).Should(BeTrue())

	requestToken := "this can be anything, so long as it's a change"
	lastScanTime := repo.Status.LastScanResult.ScanTime

	repo.Annotations = map[string]string{
		meta.ReconcileRequestAnnotation: requestToken,
	}
	g.Expect(testEnv.Update(ctx, &repo)).To(Succeed())
	g.Eventually(func() bool {
		err := testEnv.Get(ctx, objectName, &repo)
		return err == nil && repo.Status.LastScanResult.ScanTime.After(lastScanTime.Time)
	}, timeout, interval).Should(BeTrue())
	g.Expect(repo.Status.LastHandledReconcileAt).To(Equal(requestToken))
}

func TestImageRepositoryReconciler_authRegistry(t *testing.T) {
	g := NewWithT(t)

	username, password := "authuser", "authpass"
	registryServer := newAuthenticatedRegistryServer(username, password)
	defer registryServer.Close()

	// this mimics what you get if you use
	//     kubectl create secret docker-registry ...
	secret := &corev1.Secret{
		Type: "kubernetes.io/dockerconfigjson",
		StringData: map[string]string{
			".dockerconfigjson": fmt.Sprintf(`
{
  "auths": {
    %q: {
      "username": %q,
      "password": %q
    }
  }
}
`, registryName(registryServer), username, password),
		},
	}
	secret.Namespace = "default"
	secret.Name = "docker"
	g.Expect(testEnv.Create(context.Background(), secret)).To(Succeed())
	defer func() {
		g.Expect(testEnv.Delete(context.Background(), secret)).To(Succeed())
	}()

	versions := []string{"0.1.0", "0.1.1", "0.2.0", "1.0.0", "1.0.1", "1.0.2", "1.1.0-alpha"}
	imgRepo, err := loadImages(registryServer, "test-authn-"+randStringRunes(5),
		versions, remote.WithAuth(&authn.Basic{
			Username: username,
			Password: password,
		}))
	g.Expect(err).ToNot(HaveOccurred())

	repo := imagev1.ImageRepository{
		Spec: imagev1.ImageRepositorySpec{
			Interval: metav1.Duration{Duration: reconciliationInterval},
			Image:    imgRepo,
			SecretRef: &meta.LocalObjectReference{
				Name: "docker",
			},
		},
	}
	objectName := types.NamespacedName{
		Name:      "test-auth-reg-" + randStringRunes(5),
		Namespace: "default",
	}

	repo.Name = objectName.Name
	repo.Namespace = objectName.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()
	g.Expect(testEnv.Create(ctx, &repo)).To(Succeed())

	g.Eventually(func() bool {
		err := testEnv.Get(ctx, objectName, &repo)
		return err == nil && repo.Status.LastScanResult != nil
	}, timeout, interval).Should(BeTrue())
	g.Expect(repo.Status.CanonicalImageName).To(Equal(imgRepo))
	g.Expect(repo.Status.LastScanResult.TagCount).To(Equal(len(versions)))
}

func TestImageRepositoryReconciler_invalidImage(t *testing.T) {
	g := NewWithT(t)

	repo := imagev1.ImageRepository{
		Spec: imagev1.ImageRepositorySpec{
			Image: "https://example.com/repository/foo/bar:1.0.0",
		},
	}
	objectName := types.NamespacedName{
		Name:      "random",
		Namespace: "default",
	}

	repo.Name = objectName.Name
	repo.Namespace = objectName.Namespace

	ctx, cancel := context.WithTimeout(context.TODO(), contextTimeout)
	defer cancel()

	g.Expect(testEnv.Create(ctx, &repo)).To(Succeed())

	var ready *metav1.Condition
	g.Eventually(func() bool {
		_ = testEnv.Get(ctx, objectName, &repo)
		ready = apimeta.FindStatusCondition(*repo.GetStatusConditions(), meta.ReadyCondition)
		return ready != nil && ready.Reason == imagev1.ImageURLInvalidReason
	}, timeout, interval).Should(BeTrue())
	g.Expect(ready.Message).To(ContainSubstring("should not start with URL scheme"))
}
