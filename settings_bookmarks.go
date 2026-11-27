package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/route"
	"github.com/crmin/kuri/internal/settings"
)

type bookmarkRequest struct {
	Operation             string   `json:"operation"`
	ID                    string   `json:"id,omitempty"`
	Name                  string   `json:"name,omitempty"`
	KubeconfigPath        string   `json:"kubeconfig_path,omitempty"`
	KubeconfigFingerprint string   `json:"kubeconfig_fingerprint,omitempty"`
	Before                []string `json:"before,omitempty"`
	Order                 []string `json:"order,omitempty"`
	settings.BookmarkTarget
}

type bookmarkReply struct {
	Error                 string              `json:"error,omitempty"`
	Items                 []settings.Bookmark `json:"items"`
	Rows                  []bookmarkMenuRow   `json:"rows,omitempty"`
	Options               []string            `json:"options,omitempty"`
	KubeconfigPath        string              `json:"kubeconfig_path,omitempty"`
	KubeconfigFingerprint string              `json:"kubeconfig_fingerprint,omitempty"`
	Changed               bool                `json:"changed,omitempty"`
	Duplicate             bool                `json:"duplicate,omitempty"`
	Refresh               bool                `json:"refresh,omitempty"`
}

type bookmarkMenuRow struct {
	settings.Bookmark
	Label       string `json:"label"`
	URL         string `json:"url,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

func runBookmarks(ctx context.Context, home string, input io.Reader, output io.Writer) error {
	decoder := json.NewDecoder(io.LimitReader(input, 2<<20))
	decoder.DisallowUnknownFields()
	var request bookmarkRequest
	if err := decoder.Decode(&request); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("Send a single bookmark request.")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return json.NewEncoder(output).Encode(handleBookmarks(ctx, settings.NewStore(home), request))
}

func handleBookmarks(ctx context.Context, store settings.Store, req bookmarkRequest) (reply bookmarkReply) {
	var err error
	switch req.Operation {
	case "list":
		reply.Items, err = store.ReadBookmarks()
	case "menu":
		return bookmarkMenu(store)
	case "add":
		return addBookmark(ctx, store, req)
	case "remove":
		reply.Items, reply.Changed, err = store.RemoveBookmark(ctx, req.ID)
	case "reorder":
		reply.Items, reply.Changed, err = store.ReorderBookmarks(ctx, req.Before, req.Order)
	case "contexts", "namespaces", "services", "ports":
		return bookmarkOptions(ctx, store, req)
	default:
		err = errors.New("Choose a bookmark action.")
	}
	if err != nil {
		reply.Error = err.Error()
	}
	return reply
}

func bookmarkKubeconfig(store settings.Store, req bookmarkRequest) (settings.Config, *forward.Kubernetes, error) {
	cfg, err := store.Read()
	if err != nil {
		return cfg, nil, err
	}
	path := cfg.Kubernetes.ConfigPath
	if req.Operation != "contexts" && path != req.KubeconfigPath {
		return cfg, nil, errKubeconfigChanged
	}
	kube, _, err := forward.LoadKubernetes(path)
	if err != nil {
		if req.Operation != "contexts" {
			return cfg, nil, errKubeconfigChanged
		}
		return cfg, nil, err
	}
	if req.Operation != "contexts" && req.KubeconfigFingerprint != kube.Fingerprint() {
		return cfg, nil, errKubeconfigChanged
	}
	return cfg, kube, nil
}

func bookmarkOptions(ctx context.Context, store settings.Store, req bookmarkRequest) bookmarkReply {
	cfg, kube, err := bookmarkKubeconfig(store, req)
	if err != nil {
		return bookmarkFailure(err)
	}
	reply := bookmarkReply{KubeconfigPath: cfg.Kubernetes.ConfigPath, KubeconfigFingerprint: kube.Fingerprint()}
	if req.Operation == "contexts" {
		reply.Options = kube.ContextNames()
		return reply
	}
	finish, err := startBookmarkCredentials(ctx, kube)
	if err != nil {
		return bookmarkFailure(err)
	}
	defer finish()
	switch req.Operation {
	case "namespaces":
		reply.Options, err = kube.BookmarkNamespaces(ctx, req.Context)
	case "services":
		if req.Namespace == "" {
			return bookmarkFailure(errors.New("Choose a namespace."))
		}
		reply.Options, err = kube.BookmarkServices(ctx, req.Context, req.Namespace)
	case "ports":
		if !req.BookmarkTarget.Valid() {
			return bookmarkFailure(errors.New("Choose a context, namespace, and service."))
		}
		ports, getErr := kube.BookmarkPorts(ctx, req.BookmarkTarget)
		err = getErr
		for _, port := range ports {
			if port.Name != "" && route.IsTCPServicePort(port) {
				reply.Options = append(reply.Options, port.Name)
			}
		}
		slices.Sort(reply.Options)
	}
	if err != nil {
		return bookmarkLookupFailure(ctx, err)
	}
	return reply
}

func startBookmarkCredentials(ctx context.Context, kube *forward.Kubernetes) (func(), error) {
	if err := kube.StartCredentials(os.TempDir(), false); err != nil {
		return nil, err
	}
	// Removing the session directory stops the wrapper even while client-go
	// is synchronously waiting for the exec plugin to exit.
	closeSession := sync.OnceFunc(func() { _ = kube.StopCredentials() })
	stop := context.AfterFunc(ctx, closeSession)
	return func() {
		stop()
		closeSession()
	}, nil
}

func bookmarkLookupFailure(ctx context.Context, err error) bookmarkReply {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return bookmarkFailure(err)
}

func bookmarkFailure(err error) bookmarkReply {
	return bookmarkReply{Error: err.Error(), Refresh: errors.Is(err, errKubeconfigChanged)}
}

func addBookmark(ctx context.Context, store settings.Store, req bookmarkRequest) bookmarkReply {
	if !req.BookmarkTarget.Valid() {
		return bookmarkFailure(errors.New("Choose a context, namespace, and service."))
	}
	items, err := store.ReadBookmarks()
	if err != nil {
		return bookmarkFailure(err)
	}
	if req.BookmarkTarget.In(items) {
		return bookmarkReply{Duplicate: true}
	}
	cfg, kube, err := bookmarkKubeconfig(store, req)
	if err != nil {
		return bookmarkFailure(err)
	}
	finish, err := startBookmarkCredentials(ctx, kube)
	if err != nil {
		return bookmarkFailure(err)
	}
	defer finish()
	ports, err := kube.BookmarkPorts(ctx, req.BookmarkTarget)
	if err != nil {
		return bookmarkLookupFailure(ctx, err)
	}
	if _, err := route.ChooseServicePort(ports, route.Selector{PortName: req.PortName}); err != nil {
		switch {
		case req.PortName != "":
			return bookmarkFailure(errors.New("The selected TCP port is unavailable. Choose another Port Name."))
		case !slices.ContainsFunc(ports, route.IsTCPServicePort):
			return bookmarkFailure(errors.New("This service has no TCP ports. Choose another service."))
		case len(ports) > 1:
			return bookmarkFailure(errors.New("This service has multiple ports. Select a Port Name."))
		default:
			return bookmarkFailure(err)
		}
	}
	items, changed, err := store.AddBookmark(ctx, settings.Bookmark{
		Name: req.Name, KubeconfigPath: cfg.Kubernetes.ConfigPath, BookmarkTarget: req.BookmarkTarget,
	}, func() error {
		_, _, err := bookmarkKubeconfig(store, req)
		return err
	})
	if err != nil {
		return bookmarkFailure(err)
	}
	if !changed {
		return bookmarkReply{Duplicate: true}
	}
	return bookmarkReply{Items: items, Changed: true}
}

func bookmarkMenu(store settings.Store) bookmarkReply {
	items, err := store.ReadBookmarks()
	if err != nil {
		return bookmarkFailure(err)
	}
	if len(items) == 0 {
		return bookmarkReply{}
	}
	cfg, settingsErr := store.Read()
	var routes route.Contexts
	var kube *forward.Kubernetes
	var kubeErr error
	if settingsErr == nil {
		kube, _, kubeErr = forward.LoadKubernetes(cfg.Kubernetes.ConfigPath)
		if kubeErr == nil {
			routes = kube.Routes(cfg.Server, cfg.Kubernetes.ExcludedContexts, cfg.Kubernetes.Aliases())
		}
	}
	rows := make([]bookmarkMenuRow, 0, len(items))
	for _, item := range items {
		target := route.Target{Context: item.Context, Namespace: item.Namespace, Service: item.Service}
		row := bookmarkMenuRow{Bookmark: item, Label: route.ForwardLabel(target, item.PortName, item.PortName == "")}
		switch {
		case settingsErr != nil:
			row.Unavailable = "Could not load kuri settings."
		case item.KubeconfigPath != cfg.Kubernetes.ConfigPath:
			row.Unavailable = "Switch to this bookmark's kubeconfig in Settings."
		case kubeErr != nil:
			row.Unavailable = "Could not load the current kubeconfig."
		case !routes.Enabled(target.Context):
			row.Unavailable = "This context is missing or disabled in Settings."
		default:
			label, authority := routes.BookmarkAddress(target, item.PortName)
			row.Label = label
			if authority == "" {
				row.Unavailable = "This address requires an enabled domain format and a unique context alias."
			} else {
				row.URL = "http://" + authority
			}
		}
		rows = append(rows, row)
	}
	return bookmarkReply{Rows: rows}
}
