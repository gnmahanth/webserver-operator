/*
Copyright 2021.

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
	"context"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	webserverv1 "github.com/gnmahanth/webserver-operator/api/v1"
)

// ServerReconciler reconciles a Server object
type ServerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=webserver.tutorial.io,resources=servers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=webserver.tutorial.io,resources=servers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=webserver.tutorial.io,resources=servers/finalizers,verbs=update

//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=service,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Server object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *ServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logf := log.FromContext(ctx)

	var ws webserverv1.Server
	if err := r.Get(ctx, req.NamespacedName, &ws); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// check if deployment exists
	deploy := appsv1.Deployment{}
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: ws.Namespace, Name: ws.Name}, &deploy)
	if apierrors.IsNotFound(err) {
		logf.Info("deployment donot exits, create new deployment")
		deploy, err = r.buildDeployment(ws)
		if err != nil {
			logf.Error(err, "failed to build deployment")
			return ctrl.Result{}, err
		}
		if err := r.Client.Create(ctx, &deploy); err != nil {
			logf.Error(err, "failed to create deployment")
			return ctrl.Result{}, err
		}
		r.Recorder.Event(&ws, corev1.EventTypeNormal, "Created", "Created deployment "+deploy.Name)
	} else if err != nil {
		logf.Error(err, "failed to get deployment")
		return ctrl.Result{}, err
	}

	// update status
	ws.Status.ReadyReplicas = deploy.Status.ReadyReplicas
	if err := r.Client.Status().Update(ctx, &ws); err != nil {
		log.Log.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	// check and update replicas
	if *deploy.Spec.Replicas != ws.Spec.Replicas {
		logf.Info("update replica count", "old", deploy.Spec.Replicas, "new", ws.Spec.Replicas)
		deploy.Spec.Replicas = &ws.Spec.Replicas
		if err := r.Client.Update(ctx, &deploy); err != nil {
			logf.Error(err, "failed to update deployment")
			return ctrl.Result{}, err
		}
		r.Recorder.Event(&ws, corev1.EventTypeNormal, "Updated",
			"Replica count updated to "+strconv.Itoa(int(ws.Spec.Replicas))+" on deployment "+deploy.Name)
	}

	// check and update image
	for i, c := range deploy.Spec.Template.Spec.Containers {
		if c.Name != "nginx-server" {
			continue
		}
		if c.Image != ws.Spec.Image {
			logf.Info("update image ", "old", c.Image, "new", ws.Spec.Image)
			deploy.Spec.Template.Spec.Containers[i].Image = ws.Spec.Image
			if err := r.Client.Update(ctx, &deploy); err != nil {
				logf.Error(err, "failed to update deployment")
				return ctrl.Result{}, err
			}
			r.Recorder.Event(&ws, corev1.EventTypeNormal, "Updated",
				"Image updated to "+ws.Spec.Image+" on deployment "+deploy.Name)
			break
		}
	}

	// check if service exists
	svc := corev1.Service{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: ws.Namespace, Name: ws.Name}, &svc)
	if apierrors.IsNotFound(err) {
		logf.Info("service donot exists, create new service")
		svc, err = r.buildService(ws)
		if err != nil {
			logf.Error(err, "failed to build service")
			return ctrl.Result{}, err
		}
		if err := r.Client.Create(ctx, &svc); err != nil {
			logf.Error(err, "failed to create service")
			return ctrl.Result{}, err
		}
		r.Recorder.Event(&ws, corev1.EventTypeNormal, "Created", "Created service "+svc.Name)
	} else if err != nil {
		logf.Error(err, "failed to get service")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ServerReconciler) buildDeployment(server webserverv1.Server) (appsv1.Deployment, error) {
	deploy := appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      server.Name,
			Namespace: server.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &server.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"server": server.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"server": server.Name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx-server",
							Image: server.Spec.Image,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 80,
									Name:          "http",
									Protocol:      "TCP",
								},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(&server, &deploy, r.Scheme); err != nil {
		return deploy, err
	}

	return deploy, nil
}

func (r *ServerReconciler) buildService(server webserverv1.Server) (corev1.Service, error) {
	svc := corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      server.Name,
			Namespace: server.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       8080,
					Protocol:   "TCP",
					TargetPort: intstr.FromString("http"),
				},
			},
			Selector: map[string]string{"server": server.Name},
		},
	}

	// set the controller reference to be able to cleanup during delete/gc
	if err := ctrl.SetControllerReference(&server, &svc, r.Scheme); err != nil {
		return svc, err
	}

	return svc, nil
}

var (
	jobOwnerKey = ".metadata.controller"
	apiGVStr    = webserverv1.GroupVersion.String()
)

// SetupWithManager sets up the controller with the Manager.
func (r *ServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("Server")

	err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&appsv1.Deployment{},
		jobOwnerKey,
		func(obj client.Object) []string {
			deploy := obj.(*appsv1.Deployment)
			owner := metav1.GetControllerOf(deploy)
			if owner == nil {
				return nil
			}
			if owner.APIVersion != apiGVStr || owner.Kind != "Server" {
				return nil
			}
			return []string{owner.Name}
		},
	)
	if err != nil {
		return err
	}

	err = mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&corev1.Service{},
		jobOwnerKey,
		func(obj client.Object) []string {
			svc := obj.(*corev1.Service)
			owner := metav1.GetControllerOf(svc)
			if owner == nil {
				return nil
			}
			if owner.APIVersion != apiGVStr || owner.Kind != "Server" {
				return nil
			}
			return []string{owner.Name}
		},
	)
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&webserverv1.Server{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		// Watches(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForObject{}).
		// Watches(&source.Kind{Type: &corev1.Service{}}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}
