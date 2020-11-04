package dependency

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	cicdNs                 = "cicd"
	sealedSecretsSubName   = "sealed-secrets-operator-helm"
	argocdSubName          = "argocd-operator"
	sealedSecretsGroupName = "sealed-secrets-operator-group"
	argocdGroupName        = "argocd-operator-group"
)

var log = logf.Log.WithName("gitops_dependencies")

// Dependency represents an instance of GitOps dependency
type Dependency struct {
	client  client.Client
	prefix  string
	isReady wait.ConditionFunc
	log     logr.Logger
}

// NewClient create a new instance of GitOps dependencies
func NewClient(client client.Client, prefix string) *Dependency {
	return &Dependency{
		client: client,
		prefix: prefix,
		log:    log.WithName("GitOps Dependencies"),
	}
}

// Install the dependencies required by GitOps
func (d *Dependency) Install() error {
	d.log.Info("Installing GitOps dependencies")
	ctx := context.Background()

	operators := []operatorResource{newSealedSecretsOperator(d.prefix), newArgoCDOperator(d.prefix)}

	// TODO: Install each operator using a separate goroutine to improve installation performance
	for _, operator := range operators {
		ns := operator.GetNamespace()
		d.log.Info("Creating Namespace", "Namespace.Name", ns.Name)
		err := d.createResourceIfAbsent(ctx, operator.GetNamespace(), types.NamespacedName{Name: ns.Name})
		if err != nil {
			return err
		}

		operatorGroup := operator.GetOperatorGroup()
		d.log.Info("Creating OperatorGroup", "OperatorGroup.Name", operatorGroup.Name)
		err = d.createResourceIfAbsent(ctx, operator.GetOperatorGroup(), types.NamespacedName{Name: operatorGroup.Name, Namespace: operatorGroup.Namespace})
		if err != nil {
			return err
		}

		subscription := operator.GetSubscription()
		d.log.Info("Creating Subscription", "Subscription.Name", subscription.Name)
		err = d.createResourceIfAbsent(ctx, operator.GetSubscription(), types.NamespacedName{Name: subscription.Name, Namespace: subscription.Namespace})
		if err != nil {
			return err
		}

		d.log.Info("Waiting for operator to install", "Operator.Name", operator.subscription, "Operator.Namespace", operator.namespace)
		err = waitForOperator(ctx, d.client, types.NamespacedName{Name: operator.csv, Namespace: operator.namespace}, d.isReady)
		if err != nil {
			return err
		}
		d.log.Info("Operator installed successfully", "Operator.Name", operator.subscription, "Operator.Namespace", operator.namespace)
	}

	return nil
}

func isOperatorReady(ctx context.Context, client client.Client, ns types.NamespacedName) wait.ConditionFunc {
	return func() (bool, error) {
		csv := &v1alpha1.ClusterServiceVersion{}
		err := client.Get(ctx, ns, csv)
		if err != nil && !errors.IsNotFound(err) {
			return false, err
		}

		switch csv.Status.Phase {
		case v1alpha1.CSVPhaseFailed:
			return false, fmt.Errorf("Operator installation failed: %s", csv.Status.Reason)
		case v1alpha1.CSVPhaseSucceeded:
			return true, nil
		}

		return false, nil
	}
}

func waitForOperator(ctx context.Context, client client.Client, ns types.NamespacedName, waitFunc wait.ConditionFunc) error {
	if waitFunc == nil {
		waitFunc = isOperatorReady(ctx, client, ns)
	}
	// poll until waitFunc returns true, error or the timeout is reached
	return wait.PollImmediate(1*time.Second, 1*time.Minute, waitFunc)
}

func (d *Dependency) createResourceIfAbsent(ctx context.Context, obj runtime.Object, ns types.NamespacedName) error {
	err := d.client.Get(ctx, ns, obj)
	if err != nil {
		if errors.IsNotFound(err) {
			err = d.client.Create(ctx, obj)
			if err != nil {
				d.log.Error(err, "Unable to create resource", "Resource.Kind", obj.GetObjectKind(), "Resource.Name", ns.
					Name)
				return err
			}
			d.log.Info("Successfully created resource", "Resource.Kind", obj.GetObjectKind(), "Resource.Name", ns.Name, "Resource.Namespace", ns.
				Namespace)
		} else if errors.IsAlreadyExists(err) {
			d.log.Info("Resource already exists", "Resource.Kind", obj.GetObjectKind(), "Resource.Name", ns.Name)
		} else {
			return err
		}
	}
	return nil
}

func newNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func newOperatorGroup(namespace, name string) *v1.OperatorGroup {
	return &v1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1.OperatorGroupSpec{
			TargetNamespaces: []string{namespace},
		},
	}
}

func newSubscription(namespace, name string) *v1alpha1.Subscription {
	return &v1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: &v1alpha1.SubscriptionSpec{
			Channel:                "alpha",
			CatalogSource:          "community-operators",
			CatalogSourceNamespace: "openshift-marketplace",
			Package:                name,
		},
	}
}

func addPrefixIfNecessary(prefix, name string) string {
	if prefix != "" {
		return prefix + "-" + name
	}
	return name
}
