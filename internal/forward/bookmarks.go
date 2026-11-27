package forward

import (
	"context"
	"slices"

	"github.com/crmin/kuri/internal/settings"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (k *Kubernetes) BookmarkNamespaces(ctx context.Context, name string) ([]string, error) {
	client, _, err := k.client(name)
	if err != nil {
		return nil, err
	}
	return listBookmarkNames(ctx, "list namespaces", func(ctx context.Context, options metav1.ListOptions) ([]corev1.Namespace, string, error) {
		page, err := client.Namespaces().List(ctx, options)
		return page.Items, page.Continue, err
	}, func(item corev1.Namespace) string { return item.Name })
}

func (k *Kubernetes) BookmarkServices(ctx context.Context, name, namespace string) ([]string, error) {
	client, _, err := k.client(name)
	if err != nil {
		return nil, err
	}
	return listBookmarkNames(ctx, "list services", func(ctx context.Context, options metav1.ListOptions) ([]corev1.Service, string, error) {
		page, err := client.Services(namespace).List(ctx, options)
		return page.Items, page.Continue, err
	}, func(item corev1.Service) string { return item.Name })
}

func listBookmarkNames[T any](ctx context.Context, operation string,
	list func(context.Context, metav1.ListOptions) ([]T, string, error), name func(T) string) ([]string, error) {
	options := metav1.ListOptions{Limit: 200}
	names := []string{}
	for {
		items, next, err := list(ctx, options)
		if err != nil {
			return nil, kubernetesError(operation, err)
		}
		for _, item := range items {
			names = append(names, name(item))
		}
		if next == "" {
			slices.Sort(names)
			return names, nil
		}
		options.Continue = next
	}
}

func (k *Kubernetes) BookmarkPorts(ctx context.Context, target settings.BookmarkTarget) ([]corev1.ServicePort, error) {
	client, _, err := k.client(target.Context)
	if err != nil {
		return nil, err
	}
	service, err := client.Services(target.Namespace).Get(ctx, target.Service, metav1.GetOptions{})
	if err != nil {
		return nil, kubernetesError("get service ports", err)
	}
	return service.Spec.Ports, nil
}
