# Testing Guide

## Overview

This guide covers writing and running tests for MongoDB Operator, including unit tests, integration tests, and testing best practices.

## Unit Test Writing Guide

### Test Structure

Unit tests in MongoDB Operator use the standard Go testing framework:

```go
// controllers/mongodb_controller_test.go
package controllers

import (
    "context"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/client/fake"

    mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestMongoDBReconciler(t *testing.T) {
    // Setup fake client
    scheme := runtime.NewScheme()
    err := mongodbv1alpha1.AddToScheme(scheme)
    require.NoError(t, err)

    client := fake.NewClientBuilder().WithScheme(scheme).Build()

    // Create test instance
    reconciler := &MongoDBReconciler{
        Client: client,
        Scheme: scheme,
    }

    // Test case
    t.Run("Should create StatefulSet", func(t *testing.T) {
        ctx := context.Background()

        mongodb := &mongodbv1alpha1.MongoDB{
            ObjectMeta: ctrl.ObjectMeta{
                Name:      "test-mongodb",
                Namespace: "default",
            },
            Spec: mongodbv1alpha1.MongoDBSpec{
                Members: 3,
                Version: mongodbv1alpha1.MongoDBVersion{
                    Version: "8.2",
                },
            },
        }

        err := client.Create(ctx, mongodb)
        require.NoError(t, err)

        // Reconcile
        _, err = reconciler.Reconcile(ctx, ctrl.Request{
            NamespacedName: client.ObjectKeyFromObject(mongodb),
        })

        // Assertions
        assert.NoError(t, err)
    })
}
```

### Test Helpers

```go
// internal/testutil/helpers.go
package testutil

import (
    "context"
    "time"

    "sigs.k8s.io/controller-runtime/pkg/client"
)

func CreateAndWait(t *testing.T, ctx context.Context, c client.Client, obj client.Object) error {
    if err := c.Create(ctx, obj); err != nil {
        return err
    }
    return waitForObject(t, ctx, c, obj)
}

func WaitForObject(t *testing.T, ctx context.Context, c client.Client, obj client.Object) error {
    key := client.ObjectKeyFromObject(obj)
    return retry.OnError(retry.DefaultRetry, func(err error) bool {
        return !client.IgnoreNotFound(err) == nil
    }, func() error {
        return c.Get(ctx, key, obj)
    })
}

func AssertConditions(t *testing.T, obj client.Object, expectedConditions map[string]bool) {
    // Implementation for asserting conditions
}
```

## Integration Test Setup

### envtest Setup

The operator uses controller-runtime's envtest for integration tests:

```go
// controllers/suite_test.go
package controllers

import (
    "path/filepath"
    "testing"
    "time"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"
    "k8s.io/client-go/kubernetes/scheme"
    "k8s.io/client-go/rest"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/envtest"
    "sigs.k8s.io/controller-runtime/pkg/envtest/printer"
    +logf "sigs.k8s.io/controller-runtime/pkg/log"
    "sigs.k8s.io/controller-runtime/pkg/log/zap"

    mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
    "github.com/keiailab/mongodb-operator/controllers"
)

var (
    cfg       *rest.Config
    k8sClient client.Client
    testEnv   *envtest.Environment
)

func TestControllers(t *testing.T) {
    RegisterFailHandler(Fail)

    RunSpecs(t, "Controller Suite", reporter.Reporter{
        SpecReporter:       printer.NewlineReporter{},
        CurrentSpecReport: printer.NewlineReporter{},
    })
}

var _ = BeforeSuite(func() {
    logf.SetLogger(zap.New(zap.UseDevMode(true)))

    By("bootstrapping test environment")
    testEnv = &envtest.Environment{
        CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
        ErrorIfCRDPathMissing: true,
    }

    var err error
    cfg, err = testEnv.Start()
    Expect(err).NotTo(HaveOccurred())
    Expect(cfg).NotTo(BeNil())

    k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
    Expect(err).NotTo(HaveOccurred())
    Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
    By("tearing down the test environment")
    err := testEnv.Stop()
    Expect(err).NotTo(HaveOccurred())
})
```

### Writing Integration Tests

```go
// controllers/mongodb_integration_test.go
package controllers

import (
    "context"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/types"

    mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

var _ = Describe("MongoDB Controller", func() {
    Context("When creating a MongoDB", func() {
        It("Should create a StatefulSet", func() {
            ctx := context.Background()

            mongodb := &mongodbv1alpha1.MongoDB{
                ObjectMeta: metav1.ObjectMeta{
                    Name:      "test-mongodb",
                    Namespace: "default",
                },
                Spec: mongodbv1alpha1.MongoDBSpec{
                    Members: 3,
                    Version: mongodbv1alpha1.MongoDBVersion{
                        Version: "8.2",
                    },
                },
            }

            err := k8sClient.Create(ctx, mongodb)
            Expect(err).NotTo(HaveOccurred())

            // Wait for StatefulSet to be created
            statefulSet := &appsv1.StatefulSet{}
            Eventually(func() bool {
                err := k8sClient.Get(ctx, types.NamespacedName{
                    Name:      "test-mongodb",
                    Namespace: "default",
                }, statefulSet)
                return err == nil
            }, timeout, interval).Should(BeTrue())
        })
    })
})
```

## Running Tests Locally

### Run Unit Tests

```bash
# Run all tests
make test

# Run tests for specific package
go test ./controllers -v

# Run specific test
go test ./controllers -v -run TestMongoDBReconciler

# Run tests with race detector
go test ./... -race

# Run tests with coverage
go test ./... -coverprofile=coverage.out
```

### Run Integration Tests

```bash
# Install envtest
make setup-envtest

# Run integration tests
make test-integration

# Run with envtest
go test ./controllers -v --ginkgo.focus="Integration"
```

### Test with Kind

```bash
# Create Kind cluster
kind create cluster --name mongodb-operator-test

# Load Docker image
kind load docker-image mongodb-operator:test --name mongodb-operator-test

# Run E2E tests
make test-e2e

# Cleanup
kind delete cluster --name mongodb-operator-test
```

## Test Coverage Requirements

### Coverage Goals

- Overall code coverage: **70% minimum**
- Controller logic: **80% minimum**
- Resource builders: **85% minimum**
- MongoDB package: **75% minimum**

### Generating Coverage Reports

```bash
# Generate coverage for all packages
go test ./... -coverprofile=coverage.out -covermode=atomic

# View coverage in terminal
go tool cover -func=coverage.out

# Generate HTML report
go tool cover -html=coverage.out -o coverage.html

# Open in browser
open coverage.html
```

### Coverage by Package

```bash
# Coverage for specific package
go test ./controllers -coverprofile=controllers_coverage.out

# Aggregate coverage
go tool cover -func=coverage.out | grep -E "^total:|controllers/|internal/"
```

## Environment Test (envtest) Usage

### Configuration

```go
// .envtest-kubebuilder.yaml
# Optional configuration for envtest
KUBEBUILDER_ASSETS: /path/to/kubernetes/binaries
USE_EXISTING_CLUSTER: false
KUBEBUILDER_CONTROLPLANE_START_TIMEOUT: 60s
KUBEBUILDER_CONTROLPLANE_STOP_TIMEOUT: 60s
```

### Managing Test Dependencies

```bash
# Install envtest binaries
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
$(go env GOPATH)/bin/setup-envtest use latest

# Set envtest environment
export KUBEBUILDER_ASSETS=$(setup-envtest use -p path)

# Run tests with envtest
make test
```

### Test Isolation

```go
// Create namespace for each test
var _ = BeforeEach(func() {
    namespace := &corev1.Namespace{
        ObjectMeta: metav1.ObjectMeta{
            Name: "test-" + randString(5),
            Labels: map[string]string{
                "test": "integration",
            },
        },
    }

    err := k8sClient.Create(ctx, namespace)
    Expect(err).NotTo(HaveOccurred())
})

var _ = AfterEach(func() {
    // Cleanup resources
    namespace := &corev1.Namespace{}
    err := k8sClient.Get(ctx, types.NamespacedName{Name: testNamespace}, namespace)
    if err == nil {
        k8sClient.Delete(ctx, namespace)
    }
})
```

## Mocking External Dependencies

### Mocking MongoDB Client

```go
// internal/mongodb/mock_executor.go
package mongodb

import "github.com/stretchr/testify/mock"

type MockExecutor struct {
    mock.Mock
}

func (m *MockExecutor) ExecuteCommand(ctx context.Context, cmd Command) (string, error) {
    args := m.Called(ctx, cmd)
    return args.String(0), args.Error(1)
}

func (m *MockExecutor) InitiateReplicaSet(ctx context.Context, config ReplicaSetConfig) error {
    args := m.Called(ctx, config)
    return args.Error(0)
}

// Usage in test
func TestMongoDBReconciler_InitiateReplicaSet(t *testing.T) {
    mockExecutor := new(MockExecutor)
    mockExecutor.On("InitiateReplicaSet", mock.Anything, mock.Anything).Return(nil)

    reconciler := &MongoDBReconciler{
        executor: mockExecutor,
    }

    err := reconciler.initiateReplicaSet(context.Background(), testConfig)
    assert.NoError(t, err)

    mockExecutor.AssertExpectations(t)
}
```

## Continuous Testing

### Watch Mode

```bash
# Install gow
go install github.com/cosmos72/gow@latest

# Run tests on file change
gow -v ./...

# Or using entr
find . -name "*_test.go" | entr -c go test ./...
```

### Pre-commit Hooks

```bash
# .git/hooks/pre-commit
#!/bin/bash
set -e

echo "Running tests..."
go test ./...

echo "Checking coverage..."
go test ./... -coverprofile=coverage.out
COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}')
echo "Total coverage: $COVERAGE"

if (( $(echo "$COVERAGE < 70" | bc -l) )); then
    echo "Coverage below 70%"
    exit 1
fi

echo "All checks passed!"
```

## Test Best Practices

1. **Table-Driven Tests**: Use table-driven tests for multiple test cases
2. **Subtests**: Use `t.Run()` for related test cases
3. **Golden Files**: Store expected output in files for comparison
4. **Timeouts**: Set reasonable timeouts for integration tests
5. **Cleanup**: Always clean up resources in AfterEach
6. **Logging**: Use descriptive test names and logs
7. **Isolation**: Tests should not depend on each other

```go
// Example: Table-driven test
func TestMongoDBReconciler_Scale(t *testing.T) {
    tests := []struct {
        name          string
        initialMembers int
        targetMembers  int
        expectError   bool
    }{
        {
            name:          "Scale from 3 to 5",
            initialMembers: 3,
            targetMembers:  5,
            expectError:   false,
        },
        {
            name:          "Invalid scale (1 to 1)",
            initialMembers: 1,
            targetMembers:  1,
            expectError:   true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```
