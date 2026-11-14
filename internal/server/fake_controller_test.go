package server

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func installFakeController(client *fake.Clientset) {
	stsResource := appsv1.SchemeGroupVersion.WithResource("statefulsets")
	podResource := corev1.SchemeGroupVersion.WithResource("pods")

	client.PrependReactor("create", "statefulsets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		create := action.(k8stesting.CreateAction)
		sts := create.GetObject().(*appsv1.StatefulSet)
		namespace := create.GetNamespace()
		if err := client.Tracker().Create(stsResource, sts, namespace); err != nil {
			return true, nil, err
		}
		if replicas(sts) > 0 {
			ensurePod(client, sts)
		}
		return true, sts, nil
	})

	client.PrependReactor("update", "statefulsets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		update := action.(k8stesting.UpdateAction)
		sts := update.GetObject().(*appsv1.StatefulSet)
		namespace := update.GetNamespace()
		object, err := client.Tracker().Get(stsResource, namespace, sts.Name)
		if err != nil && !apierrors.IsNotFound(err) {
			return true, nil, err
		}
		var previous *appsv1.StatefulSet
		if err == nil {
			previous = object.(*appsv1.StatefulSet)
		}
		if err := client.Tracker().Update(stsResource, sts, namespace); err != nil {
			return true, nil, err
		}
		if previous == nil || replicas(previous) == 0 && replicas(sts) > 0 {
			ensurePod(client, sts)
		} else if previous != nil && replicas(previous) > 0 && replicas(sts) == 0 {
			deletePod(client, namespace, sts.Name+"-0")
		}
		return true, sts, nil
	})

	client.PrependReactor("delete", "statefulsets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		deleteAction := action.(k8stesting.DeleteAction)
		namespace := deleteAction.GetNamespace()
		if err := client.Tracker().Delete(stsResource, namespace, deleteAction.GetName()); err != nil && !apierrors.IsNotFound(err) {
			return true, nil, err
		}
		deletePod(client, namespace, deleteAction.GetName()+"-0")
		return true, nil, nil
	})

	client.PrependReactor("delete", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		deleteAction := action.(k8stesting.DeleteAction)
		namespace := deleteAction.GetNamespace()
		name := deleteAction.GetName()
		object, err := client.Tracker().Get(podResource, namespace, name)
		if apierrors.IsNotFound(err) {
			return true, nil, nil
		}
		if err != nil {
			return true, nil, err
		}
		if err := client.Tracker().Delete(podResource, namespace, name); err != nil && !apierrors.IsNotFound(err) {
			return true, nil, err
		}
		pod := object.(*corev1.Pod)
		workspace := pod.Labels["poddy.io/workspace"]
		stsObject, err := client.Tracker().Get(stsResource, namespace, "poddy-"+workspace)
		if err == nil {
			sts := stsObject.(*appsv1.StatefulSet)
			if replicas(sts) > 0 {
				ensurePod(client, sts)
			}
		}
		return true, nil, nil
	})
}

func replicas(sts *appsv1.StatefulSet) int32 {
	if sts.Spec.Replicas == nil {
		return 0
	}
	return *sts.Spec.Replicas
}

func ensurePod(client *fake.Clientset, sts *appsv1.StatefulSet) {
	namespace := sts.Namespace
	if namespace == "" {
		namespace = "poddy"
	}
	name := sts.Name + "-0"
	podResource := corev1.SchemeGroupVersion.WithResource("pods")
	if _, err := client.Tracker().Get(podResource, namespace, name); err == nil {
		return
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      sts.Spec.Template.Labels,
			Annotations: sts.Spec.Template.Annotations,
			UID:         types.UID(name),
		},
		Spec: sts.Spec.Template.Spec,
	}
	if err := client.Tracker().Create(podResource, pod, namespace); err != nil && !apierrors.IsAlreadyExists(err) {
		panic(err)
	}
}

func deletePod(client *fake.Clientset, namespace, name string) {
	podResource := corev1.SchemeGroupVersion.WithResource("pods")
	if err := client.Tracker().Delete(podResource, namespace, name); err != nil && !apierrors.IsNotFound(err) {
		panic(err)
	}
}
