//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// e2e_suite_test.go — mongodb-operator e2e 진입점 (iteration 10, Phase 1 M2).
//
// 패턴 차용: keiailab/valkey-operator/test/e2e/e2e_suite_test.go (검증된).
// 차이점:
//   - managerImage default 가 mongodb-operator:e2e-dev.
//   - Prometheus Operator CRDs 부재 (mongodb 는 ServiceMonitor 가 chart values
//     opt-in 이므로 e2e 단계에서 불필요 — 시나리오 별 BeforeAll 에서 enable).
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/keiailab/mongodb-operator/test/utils"
)

var (
	// managerImage — make docker-build 의 IMG 와 일치. IMG env 로 override 가능.
	managerImage = func() string {
		if v := os.Getenv("IMG"); v != "" {
			return v
		}
		return "harbor.keiailab.dev/keiailab/platform/mongodb-operator:e2e-dev"
	}()
	// tailerImage — PITR oplog tailer 사이드카(mongodump+aws) 이미지.
	// PITR round-trip e2e 는 operator Deployment 에 OPLOG_TAILER_IMAGE 로 이걸
	// 주입해야 tailer 가 주입된다(미주입 시 fail-open = oplog 스트리밍 없음).
	tailerImage = func() string {
		if v := os.Getenv("OPLOG_TAILER_IMG"); v != "" {
			return v
		}
		return "harbor.keiailab.dev/keiailab/platform/mongodb-operator-oplog-tailer:e2e-dev"
	}()
	shouldCleanupCertManager = false
)

// TestE2E — Ginkgo 진입점.
// 환경변수:
//   - IMG: 빌드/로드할 manager image (default harbor.keiailab.dev/keiailab/platform/mongodb-operator:e2e-dev)
//   - KIND_CLUSTER: kind cluster 이름 (default kind, valkey 호환 차용 — 단,
//     본 e2e 는 utils.LoadImageToKindClusterWithName 가 hardcoded "kind" 사용)
//   - CERT_MANAGER_INSTALL_SKIP=true: cert-manager install skip (이미 설치된 경우)
//   - KUBECTL_KUBERC: 설정 시 kubectl kuberc 활성화 (default disabled — 격리)
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting mongodb-operator e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	By("building the manager image")
	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", managerImage))
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager image")

	By("loading the manager image on Kind")
	err = utils.LoadImageToKindClusterWithName(managerImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager image into Kind")

	By("building the PITR oplog tailer image (mongodump+aws)")
	cmd = exec.Command("make", "oplog-tailer-image-build", fmt.Sprintf("OPLOG_TAILER_IMG=%s", tailerImage))
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the oplog tailer image")

	By("loading the PITR oplog tailer image on Kind")
	err = utils.LoadImageToKindClusterWithName(tailerImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the oplog tailer image into Kind")

	configureKubectlKubeRC()
	setupCertManager()
})

var _ = AfterSuite(func() {
	teardownCertManager()
})

// ensureAdminSecret 는 주어진 namespace 에 "mdb-admin" Secret 을 생성한다.
// 모든 RS/Sharded e2e 의 BeforeAll 에서 호출되며, MongoDB.spec.auth.
// adminCredentialsSecretRef.name 와 일치한다. 이미 존재하면 noop.
func ensureAdminSecret(ns string) {
	manifest := fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: mdb-admin
  namespace: %s
type: Opaque
stringData:
  username: admin
  password: changeme123
`, ns)
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, _ = utils.Run(cmd)
}

func configureKubectlKubeRC() {
	if os.Getenv("KUBECTL_KUBERC") != "true" {
		By("disabling kubectl kuberc for test isolation")
		err := os.Setenv("KUBECTL_KUBERC", "false")
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to disable kubectl kuberc")
	}
}

func setupCertManager() {
	if os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true" {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping CertManager installation (CERT_MANAGER_INSTALL_SKIP=true)\n")
		return
	}
	By("checking if CertManager is already installed")
	if utils.IsCertManagerCRDsInstalled() {
		_, _ = fmt.Fprintf(GinkgoWriter, "CertManager already installed\n")
		return
	}
	shouldCleanupCertManager = true
	By("installing CertManager")
	Expect(utils.InstallCertManager()).To(Succeed(), "Failed to install CertManager")
}

func teardownCertManager() {
	if !shouldCleanupCertManager {
		return
	}
	By("uninstalling CertManager")
	utils.UninstallCertManager()
}
