package main

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
)

func main() {
	// Instantiate loader for kubeconfig file.
	kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)

	// Determine the namespace referenced by the current context in the
	// kubeconfig file.
	namespace, _, err := kubeconfig.Namespace()
	if err != nil {
		panic(err)
	}

	// Get a rest.Config from the kubeconfig file.  This will be passed into all
	// the client objects we create.
	restconfig, err := kubeconfig.ClientConfig()
	if err != nil {
		panic(err)
	}

	// Create a Kubernetes core/v1 client.
	coreclient, err := corev1client.NewForConfig(restconfig)
	if err != nil {
		panic(err)
	}

	// CREATE a new Pod.  Its name and one container are specified.  The
	// container has an HTTP readiness probe.  Create returns the created
	// object; we store it because we'll need it when we Watch.
	var zero int64
	pod, err := coreclient.Pods(namespace).Create(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hello-openshift",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "hello-openshift",
					Image: "openshift/hello-openshift",
					ReadinessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Port: intstr.FromInt(8080),
							},
						},
					},
				},
			},
			TerminationGracePeriodSeconds: &zero,
		},
	})
	if err != nil {
		panic(err)
	}

	// WATCH for modifications made to the Pod after metadata.resourceVersion.
	watcher, err := coreclient.Pods(namespace).Watch(
		metav1.SingleObject(pod.ObjectMeta),
	)
	if err != nil {
		panic(err)
	}

	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Modified:
			pod = event.Object.(*corev1.Pod)

			// If the Pod contains a status condition Ready == True, stop
			// watching.
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady &&
					cond.Status == corev1.ConditionTrue {
					watcher.Stop()
				}
			}

		default:
			panic("unexpected event type " + event.Type)
		}
	}

	// UPDATE the Pod to add an annotation.  If the metadata.resourceVersion of
	// the Pod we send does not match the version on the server, a Conflict
	// error will be returned.  We use retry.RetryOnConflict for exponential
	// backoff and retry in this case.
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get the latest version of the Pod that the server is aware of.
		pod, err = coreclient.Pods(namespace).Get(pod.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		// Add an annotation.
		if pod.Annotations == nil {
			pod.Annotations = make(map[string]string)
		}
		pod.Annotations["testing"] = "true"

		// Try to update the Pod.
		pod, err = coreclient.Pods(namespace).Update(pod)
		return err
	})
	if err != nil {
		panic(err)
	}

	// PATCH the Pod to remove the newly added annotation.  Patching does not
	// require Update, or Conflict handling.
	patch := []byte(`[{"op":"remove","path":"/metadata/annotations/testing"}]`)
	pod, err = coreclient.Pods(namespace).Patch(pod.Name, types.JSONPatchType,
		patch)
	if err != nil {
		panic(err)
	}

	// DELETE the Pod.
	err = coreclient.Pods(namespace).Delete(pod.Name, &metav1.DeleteOptions{})
	if err != nil {
		panic(err)
	}

	// Try to delete the Pod a second time.  In this case, a NotFound error will
	// be returned.  This can be detected using errors.IsNotFound.
	err = coreclient.Pods(namespace).Delete(pod.Name, &metav1.DeleteOptions{})
	if err == nil || !errors.IsNotFound(err) {
		panic(err)
	}
}
