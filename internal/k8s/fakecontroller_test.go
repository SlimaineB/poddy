package k8s

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// installFakeController simulates the StatefulSet controller for fake clients.
// It creates the workspace pod when replicas go from 0 to 1, deletes it when
// replicas go to 0, and recreates it after an explicit pod delete.
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
		if statefulSetReplicas(sts) > 0 {
			ensureFakePod(client, sts)
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
		switch {
		case previous == nil || statefulSetReplicas(previous) == 0 && statefulSetReplicas(sts) > 0:
			ensureFakePod(client, sts)
		case previous != nil && statefulSetReplicas(previous) > 0 && statefulSetReplicas(sts) == 0:
			deleteFakePod(client, namespace, PodName(workspaceName(sts)))
		}
		return true, sts, nil
	})

	client.PrependReactor("delete", "statefulsets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		deleteAction := action.(k8stesting.DeleteAction)
		namespace := deleteAction.GetNamespace()
		object, _ := client.Tracker().Get(stsResource, namespace, deleteAction.GetName())
		if err := client.Tracker().Delete(stsResource, namespace, deleteAction.GetName()); err != nil && !apierrors.IsNotFound(err) {
			return true, nil, err
		}
		if sts, ok := object.(*appsv1.StatefulSet); ok {
			deleteFakePod(client, namespace, PodName(workspaceName(sts)))
		}
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
		stsObject, err := client.Tracker().Get(stsResource, namespace, StatefulSetName(pod.Labels[labelWorkspace]))
		if err == nil {
			sts := stsObject.(*appsv1.StatefulSet)
			if statefulSetReplicas(sts) > 0 {
				ensureFakePod(client, sts)
			}
		}
		return true, nil, nil
	})
}

func ensureFakePod(client *fake.Clientset, sts *appsv1.StatefulSet) {
	namespace := sts.Namespace
	if namespace == "" {
		namespace = "poddy"
	}
	podResource := corev1.SchemeGroupVersion.WithResource("pods")
	name := PodName(workspaceName(sts))
	if _, err := client.Tracker().Get(podResource, namespace, name); err == nil {
		return
	}
	pod := podFromStatefulSet(sts)
	pod.Namespace = namespace
	pod.UID = types.UID(name)
	if err := client.Tracker().Create(podResource, pod, namespace); err != nil && !apierrors.IsAlreadyExists(err) {
		panic(err)
	}
}

func deleteFakePod(client *fake.Clientset, namespace, name string) {
	podResource := corev1.SchemeGroupVersion.WithResource("pods")
	if err := client.Tracker().Delete(podResource, namespace, name); err != nil && !apierrors.IsNotFound(err) {
		panic(err)
	}
}
