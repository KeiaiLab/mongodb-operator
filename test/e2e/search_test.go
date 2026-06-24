//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// search_test.go — PR5: MongoDBSearch / MongoDBSearchIndex $vectorSearch round-trip e2e.
//
// MongoDB RS + MongoDBSearch(mongot sidecar) → mongot Ready → MongoDBSearchIndex(vectorSearch) →
// 문서 insert → $vectorSearch aggregate 결과 검증(in-process mongo-driver, portForward). disk-pause
// guard 로 mongot 이 data PVC 의 search-index subPath(노드 디스크 독립)를 마운트하는지 확인.
// operator 는 BeforeAll 에서 make deploy(self-contained — e2e_suite BeforeSuite 는 image build/load 만).

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/keiailab/mongodb-operator/test/utils"
)

const (
	searchNS        = "mongodb-search-e2e"
	searchMongoName = "mdb-search"
	searchCRName    = "mdb-search-srch"
	searchAdminPw   = "changeme123" // ensureAdminSecret 와 일치
	// mongotImage — pinned mongot 릴리스 태그. e2e 는 *핀된* 태그를 쓴다(operator default `:latest`
	// 가 아님): pinned 태그는 imagePullPolicy 기본값이 IfNotPresent 라 노드 pre-cache(BeforeAll)가
	// 그대로 사용됨. `:latest` 는 Always 라 매번 Docker Hub 를 치고, 1.3GB pull 이 spec 의 6분
	// Eventually 를 초과해 실패한다(발견: PR5 e2e 콜드런). MongoDBSearch.spec.version.version 과 일치.
	mongotImage = "mongodb/mongodb-community-search:0.69.1"
)

var _ = Describe("MongoDBSearch $vectorSearch Round-Trip (PR5)", Ordered, func() {
	BeforeAll(func() {
		By("deploying operator (make deploy — self-contained)")
		_, err := utils.Run(exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage)))
		Expect(err).NotTo(HaveOccurred(), "make deploy")
		By("waiting for operator controller-manager rollout (best-effort)")
		_, _ = utils.Run(exec.Command("kubectl", "-n", "mongodb-operator-system", "rollout", "status",
			"deploy/mongodb-operator-controller-manager", "--timeout=150s"))

		By("pre-caching mongot image on the kind node (avoid slow Docker Hub pull inside the timed spec)")
		// mongot 이미지(~1.3GB)를 노드 containerd 에 미리 적재한다. 핀된 :0.69.1(IfNotPresent)가 이
		// 캐시를 그대로 사용 → mongod pod 가 in-spec pull 없이 빠르게 Ready. kind 노드 이름 =
		// <KIND_CLUSTER>-control-plane. best-effort(비-kind/도커 부재 시 pod 가 직접 pull로 degrade).
		kindCluster := os.Getenv("KIND_CLUSTER")
		if kindCluster == "" {
			kindCluster = "kind"
		}
		_, _ = utils.Run(exec.Command("docker", "exec", kindCluster+"-control-plane",
			"crictl", "pull", mongotImage))

		_, _ = utils.Run(exec.Command("kubectl", "create", "ns", searchNS))
		ensureAdminSecret(searchNS)

		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: %s
  namespace: %s
spec:
  members: 1
  version:
    version: "8.2"
  storage:
    size: 2Gi
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mdb-admin
---
apiVersion: mongodb.keiailab.com/v1beta1
kind: MongoDBSearch
metadata:
  name: %s
  namespace: %s
spec:
  version:
    version: "0.69.1"
  source:
    kind: MongoDB
    mongodbResourceRef:
      name: %s
`, searchMongoName, searchNS, searchCRName, searchNS, searchMongoName)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "MongoDB + MongoDBSearch apply")
	})

	AfterAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "ns", searchNS, "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("make", "undeploy", "ignore-not-found=true"))
	})

	It("MongoDB Running + MongoDBSearch Ready (mongot sidecar)", func() {
		Eventually(func() string {
			out, _ := utils.Run(exec.Command("kubectl", "get", "mongodb", searchMongoName, "-n", searchNS,
				"-o", "jsonpath={.status.phase}"))
			return out
		}, 6*time.Minute, 10*time.Second).Should(Equal("Running"), "MongoDB RS Running")

		// MongoDBSearch Ready = mongot sidecar 컨테이너가 실제 ready(PR1 readiness 집계).
		Eventually(func() string {
			out, _ := utils.Run(exec.Command("kubectl", "get", "mongodbsearch", searchCRName, "-n", searchNS,
				"-o", "jsonpath={.status.phase}"))
			return out
		}, 6*time.Minute, 10*time.Second).Should(Equal("Ready"), "MongoDBSearch Ready (mongot sidecar ready)")
	})

	It("mongot sidecar mounts data PVC search-index subPath (disk-pause guard)", func() {
		// mongot 은 data PVC 의 search-index subPath 를 마운트해야 한다(emptyDir/노드 디스크 아님 —
		// 과거 근본원인: 노드 디스크 압박 시 mongot replication pause). VCT 불변 보존.
		out, err := utils.Run(exec.Command("kubectl", "get", "pod", searchMongoName+"-0", "-n", searchNS,
			"-o", `jsonpath={.spec.containers[?(@.name=="mongot")].volumeMounts[?(@.subPath=="search-index")].name}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(out)).NotTo(BeEmpty(),
			"mongot 컨테이너가 data PVC 의 search-index subPath 를 마운트해야(VCT 불변)")
	})

	It("$vectorSearch round-trip via mongo-driver", func() {
		By("MongoDBSearchIndex(vectorSearch, 3-dim) 생성")
		idxManifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1beta1
kind: MongoDBSearchIndex
metadata:
  name: vec-idx
  namespace: %s
spec:
  searchRef:
    name: %s
  database: testdb
  collection: items
  indexName: vector_index
  type: vectorSearch
  definitionJSON: |
    {"fields":[{"type":"vector","path":"embedding","numDimensions":3,"similarity":"cosine"}]}
`, searchNS, searchCRName)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(idxManifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "MongoDBSearchIndex apply")

		By("port-forward 127.0.0.1:47017 → mongod headless:27017")
		cancel, err := portForward(searchMongoName+"-headless", searchNS, 47017, 27017)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()

		ctx, ccancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer ccancel()
		uri := fmt.Sprintf("mongodb://admin:%s@127.0.0.1:47017/?directConnection=true", searchAdminPw)
		client, err := mongo.Connect(options.Client().ApplyURI(uri))
		Expect(err).NotTo(HaveOccurred(), "mongo connect")
		defer func() { _ = client.Disconnect(ctx) }()
		coll := client.Database("testdb").Collection("items")

		By("문서 insert (3차원 embedding)")
		_, err = coll.InsertMany(ctx, []interface{}{
			bson.M{"_id": 1, "name": "alpha", "embedding": bson.A{1.0, 0.0, 0.0}},
			bson.M{"_id": 2, "name": "beta", "embedding": bson.A{0.0, 1.0, 0.0}},
			bson.M{"_id": 3, "name": "gamma", "embedding": bson.A{0.9, 0.1, 0.0}},
		})
		Expect(err).NotTo(HaveOccurred(), "InsertMany")

		By("SearchIndex Ready(queryable) 대기")
		Eventually(func() string {
			out, _ := utils.Run(exec.Command("kubectl", "get", "mongodbsearchindex", "vec-idx", "-n", searchNS,
				"-o", "jsonpath={.status.phase}"))
			return out
		}, 5*time.Minute, 10*time.Second).Should(Equal("Ready"), "vectorSearch 인덱스 Ready(queryable)")

		By("$vectorSearch query (queryVector≈alpha) → 결과 반환 + 최상위=alpha")
		Eventually(func() (string, error) {
			pipeline := mongo.Pipeline{
				{{Key: "$vectorSearch", Value: bson.D{
					{Key: "index", Value: "vector_index"},
					{Key: "path", Value: "embedding"},
					{Key: "queryVector", Value: bson.A{1.0, 0.0, 0.0}},
					{Key: "numCandidates", Value: 10},
					{Key: "limit", Value: 2},
				}}},
			}
			cur, e := coll.Aggregate(ctx, pipeline)
			if e != nil {
				return "", e
			}
			var results []bson.M
			if e := cur.All(ctx, &results); e != nil {
				return "", e
			}
			if len(results) == 0 {
				return "", nil // mongot 동기화 진행 중 — retry
			}
			name, _ := results[0]["name"].(string)
			return name, nil
		}, 3*time.Minute, 10*time.Second).Should(Equal("alpha"),
			"$vectorSearch 최상위 결과가 queryVector 와 동일한 alpha 여야(cosine 유사도)")
	})
})
