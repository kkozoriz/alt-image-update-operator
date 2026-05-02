package controller

import (
	"fmt"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"testing"
	"time"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	runtimeclientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	envtestClient     client.Client
	envtestScheme     *apiruntime.Scheme
	envtestSkipReason string
	testEnv           *envtest.Environment
)

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	scheme := runtimeclientgoscheme.Scheme
	if err := securityv1alpha1.AddToScheme(scheme); err != nil {
		fmt.Fprintf(os.Stderr, "add security scheme: %v\n", err)
		os.Exit(1)
	}
	envtestScheme = scheme

	assetsDir, ok := findEnvtestAssets()
	if !ok {
		envtestSkipReason = "envtest tests skipped: kube-apiserver, etcd, and kubectl binaries are missing; set KUBEBUILDER_ASSETS or TEST_ASSET_* to run them"
		fmt.Fprintln(os.Stderr, envtestSkipReason)
		os.Exit(m.Run())
	}

	testEnv = &envtest.Environment{
		Scheme:                   scheme,
		CRDDirectoryPaths:        []string{filepath.Join(projectRootForEnvtest(), "config", "crd", "bases")},
		ErrorIfCRDPathMissing:    true,
		BinaryAssetsDirectory:    assetsDir,
		ControlPlaneStartTimeout: 60 * time.Second,
		ControlPlaneStopTimeout:  60 * time.Second,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start envtest control plane: %v\n", err)
		os.Exit(1)
	}

	envtestClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create envtest client: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	if err := testEnv.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop envtest control plane: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func newEnvtestReconciler(t *testing.T) *AltImageUpdatePolicyReconciler {
	t.Helper()
	requireEnvtest(t)

	return &AltImageUpdatePolicyReconciler{
		Client:         envtestClient,
		Scheme:         envtestScheme,
		CheckLogReader: &fakeCheckJobLogReader{},
		Recorder:       record.NewFakeRecorder(100),
		Metrics:        &fakeMetricsRecorder{},
	}
}

func requireEnvtest(t *testing.T) {
	t.Helper()
	if envtestClient == nil {
		t.Skip(envtestSkipReason)
	}
}

func projectRootForEnvtest() string {
	_, file, _, ok := stdruntime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func findEnvtestAssets() (string, bool) {
	if envtestOverrideBinariesPresent() {
		return "", true
	}

	candidates := []string{
		os.Getenv("KUBEBUILDER_ASSETS"),
		"/usr/local/kubebuilder/bin",
	}
	if setupEnvtestDir, err := envtest.SetupEnvtestDefaultBinaryAssetsDirectory(); err == nil {
		candidates = append(candidates, setupEnvtestDir)
		if nestedDir := findNestedEnvtestAssets(setupEnvtestDir); nestedDir != "" {
			candidates = append(candidates, nestedDir)
		}
	}

	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if envtestAssetDirHasBinaries(dir) {
			return dir, true
		}
	}
	return "", false
}

func envtestOverrideBinariesPresent() bool {
	for _, envName := range []string{"TEST_ASSET_KUBE_APISERVER", "TEST_ASSET_ETCD", "TEST_ASSET_KUBECTL"} {
		path := os.Getenv(envName)
		if path == "" || !fileExists(path) {
			return false
		}
	}
	return true
}

func envtestAssetDirHasBinaries(dir string) bool {
	for _, name := range []string{"kube-apiserver", "etcd", "kubectl"} {
		if !fileExists(filepath.Join(dir, name)) {
			return false
		}
	}
	return true
}

func findNestedEnvtestAssets(root string) string {
	var found string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || found != "" || entry.IsDir() || entry.Name() != "kube-apiserver" {
			return nil
		}
		dir := filepath.Dir(path)
		if envtestAssetDirHasBinaries(dir) {
			found = dir
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func envtestHasAltImageUpdatePolicyCRD(t *testing.T) {
	t.Helper()
	requireEnvtest(t)

	resources, err := envtestClient.RESTMapper().ResourcesFor(schema.GroupVersionResource{
		Group:    securityv1alpha1.GroupVersion.Group,
		Version:  securityv1alpha1.GroupVersion.Version,
		Resource: "altimageupdatepolicies",
	})
	if err != nil {
		t.Fatalf("discover AltImageUpdatePolicy CRD: %v", err)
	}
	if len(resources) == 0 || resources[0].Resource != "altimageupdatepolicies" {
		t.Fatalf("discovered resources = %#v, want altimageupdatepolicies", resources)
	}
}

func envtestSchemeHasCoreTypes(t *testing.T) {
	t.Helper()

	for _, object := range []client.Object{
		&securityv1alpha1.AltImageUpdatePolicy{},
		&appsv1.Deployment{},
		&corev1.ConfigMap{},
		&batchv1.Job{},
	} {
		if _, _, err := envtestScheme.ObjectKinds(object); err != nil {
			t.Fatalf("scheme does not know %T: %v", object, err)
		}
	}
}
