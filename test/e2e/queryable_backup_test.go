//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.
*/

// queryable_backup_test.go — F46-F47 (cycle 9) queryable backup + verification e2e stub.

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
	qbackupNamespace = "mongodb-qbackup-e2e"
	verifyCRName     = "mdb-backup-verify"
)

var _ = Describe("MongoDBBackupVerification (cycle 9 / F47-F50)", Ordered, func() {
	BeforeAll(func() {
		cmd := exec.Command("kubectl", "create", "namespace", qbackupNamespace)
		_, _ = utils.Run(cmd)
	})
	AfterAll(func() {
		cmd := exec.Command("kubectl", "delete", "namespace", qbackupNamespace, "--ignore-not-found", "--wait=false")
		_, _ = utils.Run(cmd)
	})

	It("CRD accepts MongoDBBackupVerification with sample queries", func() {
		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackupVerification
metadata:
  name: %s
  namespace: %s
spec:
  backupRef:
    name: existing-backup
  cleanupOnSuccess: true
  sampleQueries:
    - db: prod
      collection: users
      filterJSON: '{"active":true}'
      minExpectedDocs: 1
    - db: prod
      collection: orders
      filterJSON: "{}"
      minExpectedDocs: 100
`, verifyCRName, qbackupNamespace)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		out, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "apply: %s", out)
	})
})
