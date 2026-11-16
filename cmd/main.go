// SPDX-License-Identifier: Apache-2.0
package main

import (
	"flag"
	"os"

	operations "github.com/madhifallah/guarded-batch-operator/api/v1alpha1"
	"github.com/madhifallah/guarded-batch-operator/internal/controller"
	"github.com/madhifallah/guarded-batch-operator/internal/policy"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	var namespace, metricsAddress, healthAddress string
	var leaderElection bool
	flag.StringVar(&namespace, "namespace", "guarded-batch-operator-system", "Namespace containing requests, trusted configuration and Jobs")
	flag.StringVar(&metricsAddress, "metrics-bind-address", "127.0.0.1:8080", "Controller metrics address; use 0 to disable")
	flag.StringVar(&healthAddress, "health-probe-bind-address", ":8081", "Health and readiness endpoint address")
	flag.BoolVar(&leaderElection, "leader-elect", false, "Elect one active controller")
	logOptions := zap.Options{}
	logOptions.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&logOptions)))
	log := ctrl.Log.WithName("setup")
	if namespace == "" {
		log.Error(nil, "namespace must not be empty")
		os.Exit(1)
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(operations.AddToScheme(scheme))
	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		Cache:                   cache.Options{DefaultNamespaces: map[string]cache.Config{namespace: {}}},
		Metrics:                 metricsserver.Options{BindAddress: metricsAddress},
		HealthProbeBindAddress:  healthAddress,
		LeaderElection:          leaderElection,
		LeaderElectionNamespace: namespace,
		LeaderElectionID:        "guarded-batch-operator.example.org",
	})
	if err != nil {
		log.Error(err, "create manager")
		os.Exit(1)
	}
	reconciler := &controller.GuardedRunReconciler{Client: manager.GetClient(), Scheme: scheme,
		Policy: &policy.Engine{}, Recorder: manager.GetEventRecorderFor("guarded-batch-operator")}
	if err := reconciler.SetupWithManager(manager); err != nil {
		log.Error(err, "register controller")
		os.Exit(1)
	}
	if err := manager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "register health check")
		os.Exit(1)
	}
	if err := manager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "register readiness check")
		os.Exit(1)
	}
	if err := manager.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "run manager")
		os.Exit(1)
	}
}
