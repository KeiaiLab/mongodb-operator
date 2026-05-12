//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.
*/

// federation_test.go — F37 (cycle 5) Federation CRD e2e stub.
// 실 cross-cluster bind 는 cycle 8 강화.

package e2e

import (
	"fmt"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/keiailab/mongodb-operator/test/utils"
)

const (
	fedNamespace = "mongodb-federation-e2e"
	fedCRName    = "mdb-fed"
)

var _ = Describe("MongoDBFederation CRD (cycle 5 / F33-F37)", Ordered, func() {
	BeforeAll(func() {
		cmd := exec.Command("kubectl", "create", "namespace", fedNamespace)
		_, _ = utils.Run(cmd)
	})

	AfterAll(func() {
		cmd := exec.Command("kubectl", "delete", "namespace", fedNamespace, "--ignore-not-found", "--wait=false")
		_, _ = utils.Run(cmd)
	})

	It("CRD accepts 2-region MongoDBFederation", func() {
		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBFederation
metadata:
  name: %s
  namespace: %s
spec:
  version:
    version: "8.2"
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: fed-admin
  regions:
    - name: us-west
      clusterKubeConfigRef:
        name: us-west-kubeconfig
      members: 2
      priority: "1.0"
      zone: us-west-2
      storage:
        size: 20Gi
    - name: eu-central
      clusterKubeConfigRef:
        name: eu-central-kubeconfig
      members: 2
      priority: "0.5"
      zone: eu-central-1
      storage:
        size: 20Gi
`, fedCRName, fedNamespace)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		out, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "apply: %s", out)
	})

	It("Status.Phase becomes Bootstrapping then progresses", func() {
		Eventually(func() string {
			cmd := exec.Command("kubectl", "-n", fedNamespace, "get", "mongodbfederation", fedCRName,
				"-o", "jsonpath={.status.phase}")
			out, _ := utils.Run(cmd)
			return strings.TrimSpace(out)
		}, "2m", "5s").Should(BeElementOf("Bootstrapping", "Pending", "Synced"))
	})
})
