package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/crmin/kuri/internal/accesslog"
	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/route"
)

var ErrShutdown = errors.New("server shutdown interrupted the request")

type connector interface {
	AcquireRequest(context.Context, route.Request) (*forward.Lease, func(), error)
}

type eventWriter interface {
	Write(accesslog.Event) error
}

type Handler struct {
	contexts route.Contexts
	forwards connector
	logger   eventWriter
	logError func(error)
}

func New(contexts route.Contexts, forwards connector, logger eventWriter, logError func(error)) *Handler {
	return &Handler{contexts, forwards, logger, logError}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	success := false
	event := accesslog.Event{Time: time.Now(), Event: "request", Domain: limit(r.Host), Success: &success}
	defer func() {
		if errors.Is(context.Cause(r.Context()), ErrShutdown) {
			success = false
			event.Connection = nil
			event.Reason = ErrShutdown.Error()
		}
		if err := h.logger.Write(event); err != nil {
			h.logError(err)
		}
	}()

	target, err := h.contexts.Parse(r.Host)
	if err != nil {
		event.Reason = err.Error()
		http.Error(w, event.Reason, http.StatusBadRequest)
		return
	}
	event.Domain = target.Host
	lease, release, err := h.forwards.AcquireRequest(r.Context(), target)
	if err != nil {
		event.Reason = limit(err.Error())
		http.Error(w, "Could not connect to the Kubernetes service.", http.StatusBadGateway)
		return
	}
	defer release()

	upstream := &url.URL{Scheme: "http", Host: lease.Address()}
	reverse := httputil.ReverseProxy{
		Transport: lease,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(upstream)
			request.Out.URL.RawQuery = request.In.URL.RawQuery
			request.Out.Host = request.In.Host
			request.SetXForwarded()
		},
		ModifyResponse: func(*http.Response) error {
			success = true
			dest := lease.Destination
			id, _ := json.Marshal(dest.Target)
			event.Connection = &accesslog.Connection{
				ConfigID: fmt.Sprintf("%x", sha256.Sum256(id)),
				Alias:    target.Host, Context: dest.Context,
				LocalAddr: "127.0.0.1", LocalPort: lease.LocalPort, Namespace: dest.Namespace,
				Protocol: "tcp", RemotePort: dest.Port, Service: dest.Service,
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			event.Reason = "upstream connection failed"
			switch {
			case errors.Is(err, context.Canceled):
				event.Reason += ": request canceled"
			case errors.Is(err, context.DeadlineExceeded):
				event.Reason += ": request timed out"
			}
			http.Error(w, "Could not reach the upstream HTTP service.", http.StatusBadGateway)
		},
	}
	reverse.ServeHTTP(w, r)
}

func limit(value string) string {
	if len(value) <= 2048 {
		return value
	}
	return strings.ToValidUTF8(value[:2048], "") + "..."
}
