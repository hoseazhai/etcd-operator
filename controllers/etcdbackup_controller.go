/*
Copyright 2021 hoseazhai.

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
	"fmt"
	"github.com/prometheus/common/log"
	"html/template"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	etcdv1alpha1 "github.com/hoseazhai/etcd-operator/api/v1alpha1"
)

// EtcdBackupReconciler reconciles a EtcdBackup object
type EtcdBackupReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	BackupImage string
}

type backupState struct {
	backup  *etcdv1alpha1.EtcdBackup
	actual  *backupStateContainer
	desired *backupStateContainer
}

type backupStateContainer struct {
	pod *corev1.Pod
}

func (r *EtcdBackupReconciler) setStateActual(ctx context.Context, state *backupState) error {
	var actual backupStateContainer
	key := client.ObjectKey{
		Name:      state.backup.Name,
		Namespace: state.backup.Namespace,
	}

	actual.pod = &corev1.Pod{}
	if err := r.Get(ctx, key, actual.pod); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("getting pod error: %s", err)
		}
		actual.pod = nil
	}
	state.actual = &actual
	return nil
}

func (r *EtcdBackupReconciler) setStateDesired(ctx context.Context, state *backupState) error {
	var desired backupStateContainer
	pod, err := PodForBackup(state.backup, r.BackupImage)
	if err != nil {
		return err
	}

	if err := controllerutil.SetControllerReference(state.backup, pod, r.Scheme); err != nil {
		return fmt.Errorf("setting controller reference error: %s", err)
	}
	desired.pod = pod

	state.desired = &desired
	return nil
}

func (r *EtcdBackupReconciler) getState(ctx context.Context, req ctrl.Request) (*backupState, error) {
	var state backupState

	state.backup = &etcdv1alpha1.EtcdBackup{}
	if err := r.Get(ctx, req.NamespacedName, state.backup); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, fmt.Errorf("getting backup object error: %s", err)
		}
		state.backup = nil
		return &state, nil
	}

	if err := r.setStateActual(ctx, &state); err != nil {
		return nil, fmt.Errorf("setting actual state error: %s", err)
	}

	if err := r.setStateDesired(ctx, &state); err != nil {
		return nil, fmt.Errorf("setting Desired state error: %s", err)
	}

	return &state, nil

}

func PodForBackup(backup *etcdv1alpha1.EtcdBackup, image string) (*corev1.Pod, error) {
	var secretRef *corev1.SecretEnvSource
	var backupEndpoint, backupURL string
	if backup.Spec.StorageType == etcdv1alpha1.BackupStorageTypeS3 {
		backupEndpoint = backup.Spec.S3.Endpoint
		templ, err := template.New("template").Parse(backup.Spec.S3.Path)
		if err != nil {
			return nil, err
		}
		var objectURL strings.Builder
		if err := templ.Execute(&objectURL, backup); err != nil {
			return nil, err
		}
		backupURL = fmt.Sprintf("%s://%s", backup.Spec.StorageType, objectURL.String())
		secretRef = &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: backup.Spec.S3.Secret,
			},
		}
	} else {
		// TODO
		secretRef = &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: backup.Spec.S3.Secret,
			},
		}
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backup.Name,
			Namespace: backup.Namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				corev1.Container{
					Name:  "etcd-backup",
					Image: image,
					Args: []string{
						"--etcd-url", backup.Spec.EtcdUrl,
						"--backup-url", backupURL,
					},
					Env: []corev1.EnvVar{
						{
							Name:  "ENDPOINT",
							Value: backupEndpoint,
						},
					},
					EnvFrom: []corev1.EnvFromSource{
						{
							SecretRef: secretRef,
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}, nil
}

// +kubebuilder:rbac:groups=etcd.jepaas.com,resources=etcdbackups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=etcd.jepaas.com,resources=etcdbackups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch

func (r *EtcdBackupReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	_ = r.Log.WithValues("etcdbackup", req.NamespacedName)

	// your logic here
	state, err := r.getState(ctx, req)
	if err != nil {
		return ctrl.Result{}, err
	}

	var action Action

	switch {
	case state.backup == nil:
		log.Info("Backup object not found")
	case !state.backup.DeletionTimestamp.IsZero():
		log.Info("Backup object has been deleted")
	case state.backup.Status.Phase == "":
		log.Info("Backup starting...")
		newBackup := state.backup.DeepCopy()
		newBackup.Status.Phase = etcdv1alpha1.EtcdBackupPhaseBackingUp
		action = &PatchStatus{client: r.Client, original: state.backup, new: newBackup}
	case state.backup.Status.Phase == etcdv1alpha1.EtcdBackupPhaseFailed:
		log.Info("Backup has failed. Ignoring...")
	case state.backup.Status.Phase == etcdv1alpha1.EtcdBackupPhaseCompleted:
		log.Info("Backup has completed. Ignoring...")
	case state.actual.pod == nil:
		log.Info("Backup Pod not exists. creating...")
		action = &CreateObject{client: r.Client, obj: state.desired.pod}
		r.Recorder.Event(state.backup, corev1.EventTypeNormal, EventReasonSuccessfulCreate,
			fmt.Sprintf("Create Pod: %s", state.desired.pod.Name))
	case state.actual.pod.Status.Phase == corev1.PodFailed:
		log.Info("Backup Pod failed.")
		newBackup := state.backup.DeepCopy()
		newBackup.Status.Phase = etcdv1alpha1.EtcdBackupPhaseFailed
		action = &PatchStatus{client: r.Client, original: state.backup, new: newBackup}
		r.Recorder.Event(state.backup, corev1.EventTypeWarning, EventReasonBackupFailed,
			fmt.Sprintf("Backup failed. See backup pod: %s for detail information.", state.desired.pod.Name))
	case state.actual.pod.Status.Phase == corev1.PodSucceeded:
		log.Info("Backup Pod success.")
		newBackup := state.backup.DeepCopy()
		newBackup.Status.Phase = etcdv1alpha1.EtcdBackupPhaseCompleted
		action = &PatchStatus{client: r.Client, original: state.backup, new: newBackup}
		r.Recorder.Event(state.backup, corev1.EventTypeNormal, EventReasonBackupSucceeded,
			fmt.Sprintf("Create Pod: %s completed successfully", state.desired.pod.Name))
	}

	if action != nil {
		if err := action.Execute(ctx); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *EtcdBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&etcdv1alpha1.EtcdBackup{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
