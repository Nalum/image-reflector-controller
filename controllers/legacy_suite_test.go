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
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/dgraph-io/badger/v3"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1beta1"
	"github.com/fluxcd/image-reflector-controller/internal/database"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var k8sMgr ctrl.Manager

var imageRepoReconciler *ImageRepositoryReconciler
var imagePolicyReconciler *ImagePolicyReconciler
var ginkgoTestEnv *envtest.Environment
var badgerDir string
var badgerDB *badger.DB

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func(done Done) {
	ctrl.SetLogger(
		zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)),
	)

	By("bootstrapping test environment")
	ginkgoTestEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	var err error
	cfg, err = ginkgoTestEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	err = imagev1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	k8sMgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	badgerDir, err = ioutil.TempDir(os.TempDir(), "badger")
	Expect(err).ToNot(HaveOccurred())
	badgerDB, err = badger.Open(badger.DefaultOptions(badgerDir))
	Expect(err).ToNot(HaveOccurred())

	imageRepoReconciler = &ImageRepositoryReconciler{
		Client:   k8sMgr.GetClient(),
		Scheme:   scheme.Scheme,
		Database: database.NewBadgerDatabase(badgerDB),
	}
	Expect(imageRepoReconciler.SetupWithManager(k8sMgr, ImageRepositoryReconcilerOptions{})).To(Succeed())

	imagePolicyReconciler = &ImagePolicyReconciler{
		Client:   k8sMgr.GetClient(),
		Scheme:   scheme.Scheme,
		Database: database.NewBadgerDatabase(badgerDB),
	}
	Expect(imagePolicyReconciler.SetupWithManager(k8sMgr, ImagePolicyReconcilerOptions{})).To(Succeed())

	// this must be started for the caches to be running, and thereby
	// for the client to be usable.
	go func() {
		err = k8sMgr.Start(ctx)
		Expect(err).ToNot(HaveOccurred())
	}()

	k8sClient = k8sMgr.GetClient()
	Expect(k8sClient).ToNot(BeNil())

	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := ginkgoTestEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
	Expect(badgerDB.Close()).ToNot(HaveOccurred())
	Expect(os.RemoveAll(badgerDir)).ToNot(HaveOccurred())
})
