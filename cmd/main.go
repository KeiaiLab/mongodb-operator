/*
Copyright 2024 Keiailab.

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

package main

import (
	"crypto/tls"
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/controller"
	webhookv1alpha1 "github.com/keiailab/mongodb-operator/internal/webhook/v1alpha1"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(mongodbv1alpha1.AddToScheme(scheme))
	utilruntime.Must(mongodbv1beta1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var enableShardedController bool
	var enableBackupController bool
	var enableAutoscaling bool
	var enableWebhooks bool
	var enableFederationController bool
	var enableInsightsController bool
	var enableClusterGroupController bool
	var leaderElectionNamespace string
	var tlsOpts []func(*tls.Config)

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	// 기본값 true — 단일 replica 환경에서도 leader-election lease를 보유하게 해
	// pod restart 시 reconcile 정지 창을 최소화. 멀티 replica HA 시 필수.
	// 사용자가 명시적으로 비활성화하려면 --leader-elect=false.
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&leaderElectionNamespace, "leader-election-namespace", "",
		"Namespace for the leader election lease object. Defaults to the pod namespace.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	// Feature gates — v1.3.2-beta.x carve-out. 베타에서는 모두 false 기본.
	// 여기서 false면 reconciler 자체가 등록되지 않아 controller log 오염도 막음.
	// helm chart의 features.* 게이트에서 true 설정 시 args로 주입되어 활성화.
	flag.BoolVar(&enableShardedController, "enable-sharded-controller", false,
		"Enable MongoDBSharded reconciler. Beta default: false (carve-out scope).")
	flag.BoolVar(&enableBackupController, "enable-backup-controller", false,
		"Enable MongoDBBackup reconciler. Beta default: false (carve-out scope, no automated tests).")
	flag.BoolVar(&enableAutoscaling, "enable-autoscaling", false,
		"Enable HorizontalPodAutoscaler reconciliation. Beta default: false (carve-out scope, drift mutex absent).")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", false,
		"Enable validating admission webhooks for MongoDB / MongoDBSharded. Default false — cert-manager 의존성으로 helm chart 게이트로 활성화.")
	flag.BoolVar(&enableFederationController, "enable-federation-controller", false,
		"Enable MongoDBFederation reconciler (cycle 5, skeleton). Default false — cross-cluster bind cycle 8+ 강화 후 활성화.")
	flag.BoolVar(&enableInsightsController, "enable-insights-controller", false,
		"Enable MongoDBInsights advisory reconciler (cycle 7, skeleton). Default false — analysis engine cycle 9 강화 후.")
	flag.BoolVar(&enableClusterGroupController, "enable-clustergroup-controller", false,
		"Enable MongoDBClusterGroup reconciler (cycle 8, skeleton). Default false — cross-cluster propagation cycle 9+ 강화 후.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being affected by the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsServerOptions,
		WebhookServer:           webhookServer,
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        "mongodb.keiailab.com",
		LeaderElectionNamespace: leaderElectionNamespace,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Setup MongoDB controller (always enabled — ReplicaSet은 베타 scope)
	if err = (&controller.MongoDBReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		EnableAutoscaling: enableAutoscaling,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MongoDB")
		os.Exit(1)
	}

	// Setup MongoDBSharded controller — feature gate
	if enableShardedController {
		if err = (&controller.MongoDBShardedReconciler{
			Client:            mgr.GetClient(),
			Scheme:            mgr.GetScheme(),
			EnableAutoscaling: enableAutoscaling,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MongoDBSharded")
			os.Exit(1)
		}
	} else {
		setupLog.Info("MongoDBSharded controller disabled by feature gate (--enable-sharded-controller=false)")
	}

	// Setup MongoDBBackup controller — feature gate
	if enableBackupController {
		if err = (&controller.MongoDBBackupReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MongoDBBackup")
			os.Exit(1)
		}

		// F03 (cycle 1): PITR oplog uploader controller. backup controller 와
		// 같은 feature gate 로 활성. MongoDB / MongoDBSharded 의 PITREnabled
		// 변화에 reaction.
		if err = (&controller.OplogUploaderReconciler{
			Client: mgr.GetClient(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "OplogUploader")
			os.Exit(1)
		}
		// F48-F50 (cycle 9): BackupVerification controller.
		if err = (&controller.MongoDBBackupVerificationReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MongoDBBackupVerification")
			os.Exit(1)
		}
	} else {
		setupLog.Info("MongoDBBackup controller disabled by feature gate (--enable-backup-controller=false)")
	}

	// F33-F37 (cycle 5): MongoDBFederation reconciler feature gate.
	if enableFederationController {
		if err = (&controller.MongoDBFederationReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MongoDBFederation")
			os.Exit(1)
		}
	} else {
		setupLog.Info("MongoDBFederation controller disabled by feature gate (--enable-federation-controller=false)")
	}

	// F51-F55 (cycle 7): MongoDBInsights advisory reconciler.
	if enableInsightsController {
		if err = (&controller.MongoDBInsightsReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MongoDBInsights")
			os.Exit(1)
		}
	} else {
		setupLog.Info("MongoDBInsights controller disabled by feature gate (--enable-insights-controller=false)")
	}

	// F56-F60 (cycle 8): MongoDBClusterGroup reconciler.
	if enableClusterGroupController {
		if err = (&controller.MongoDBClusterGroupReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MongoDBClusterGroup")
			os.Exit(1)
		}
	} else {
		setupLog.Info("MongoDBClusterGroup controller disabled by feature gate (--enable-clustergroup-controller=false)")
	}

	// Autoscaling 게이트는 reconciler 내부에서 enableAutoscaling 검사 — 현재 cmd 단에서는
	// log 표시만. (HPA reconcile 로직 자체는 mongodb_controller / mongodbsharded_controller 내부에 있음)
	if !enableAutoscaling {
		setupLog.Info("HorizontalPodAutoscaler reconciliation disabled by feature gate (--enable-autoscaling=false)")
	}

	// it45 — admission webhook 등록 (validating only, no mutating).
	if enableWebhooks {
		if err = webhookv1alpha1.SetupMongoDBWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "MongoDB")
			os.Exit(1)
		}
		if err = webhookv1alpha1.SetupMongoDBShardedWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "MongoDBSharded")
			os.Exit(1)
		}
		setupLog.Info("admission webhooks enabled (MongoDB / MongoDBSharded)")
	} else {
		setupLog.Info("admission webhooks disabled by feature gate (--enable-webhooks=false)")
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
