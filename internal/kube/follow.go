package kube

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type LogStreamer interface {
	StreamLogs(context.Context, string, string, string, *time.Time, ...LogOptions) (io.ReadCloser, error)
}

func (c *Client) StreamLogs(ctx context.Context, ns, pod, container string, since *time.Time, limits ...LogOptions) (io.ReadCloser, error) {
	options := logRequest(200, limits)
	options.Container, options.Follow = container, true
	if since != nil {
		options.SinceTime = &metav1.Time{Time: *since}
		options.TailLines = nil
	}
	return c.streamCore.Pods(ns).GetLogs(pod, options).Stream(ctx)
}

type logSource struct{ uid, ns, pod, container string }

func (s logSource) key() string    { return s.uid + ":" + s.container }
func (s logSource) prefix() string { return "[" + s.ns + "/" + s.pod + ":" + s.container + "] " }

// FollowWorkload reconciles new/removed pods while keeping each container's
// stream bound to its original UID. Callbacks are serialized. Buffering lives
// in the caller, which can consume continuously while its viewport is paused.
func FollowWorkload(ctx context.Context, b Backend, target unstructured.Unstructured, container string, emit func(string), opened ...func()) error {
	return FollowWorkloadWithOptions(ctx, b, target, container, LogOptions{}, emit, opened...)
}

func FollowWorkloadWithOptions(ctx context.Context, b Backend, target unstructured.Unstructured, container string, options LogOptions, emit func(string), opened ...func()) error {
	stream, ok := b.(LogStreamer)
	if !ok {
		return fmt.Errorf("this backend does not support live logs")
	}
	var output sync.Mutex
	send := func(line string) {
		output.Lock()
		defer output.Unlock()
		if ctx.Err() == nil {
			if target.GetKind() == "Pod" && container != "*" {
				line = strings.TrimPrefix(line, "["+target.GetNamespace()+"/"+target.GetName()+":"+container+"] ")
			}
			// The log view sanitizes terminal controls before buffering output.
			emit(line)
		}
	}
	active := map[string]context.CancelFunc{}
	var workers sync.WaitGroup
	defer func() {
		for _, cancel := range active {
			cancel()
		}
		workers.Wait()
	}()
	lastError := ""
	limitedBefore := false
	var terminalError error
	reconcile := func() {
		var pods []unstructured.Unstructured
		var err error
		r, _ := Resolve(Builtins(), "pods")
		if target.GetKind() != "Pod" {
			if reader, ok := b.(Editable); ok {
				resource, resolveErr := Resolve(Builtins(), strings.ToLower(target.GetKind()))
				if resolveErr == nil {
					request, cancel := context.WithTimeout(ctx, 15*time.Second)
					fresh, getErr := reader.Get(request, resource, target.GetNamespace(), target.GetName())
					cancel()
					if getErr != nil || fresh.GetUID() != target.GetUID() {
						if getErr == nil || apierrors.IsNotFound(getErr) {
							terminalError = fmt.Errorf("workload disappeared or was replaced; close logs and select it again")
						}
						for key, cancel := range active {
							cancel()
							delete(active, key)
						}
						message := "workload disappeared or was replaced; close logs and select it again"
						if getErr != nil {
							message = getErr.Error()
						}
						if lastError != message {
							send("[kqt] " + message)
							lastError = message
						}
						return
					}
				}
			}
		}
		if target.GetKind() == "Pod" {
			if reader, ok := b.(Editable); ok {
				request, cancel := context.WithTimeout(ctx, 15*time.Second)
				fresh, getErr := reader.Get(request, r, target.GetNamespace(), target.GetName())
				cancel()
				err = getErr
				if err == nil && fresh.GetUID() != target.GetUID() {
					err = fmt.Errorf("pod was replaced; close logs and select it again")
					terminalError = err
				}
				if apierrors.IsNotFound(err) {
					terminalError = err
				}
				if err == nil {
					pods = []unstructured.Unstructured{fresh}
				}
			} else {
				pods = []unstructured.Unstructured{target}
			}
		} else {
			selector, _, valid := PodSelector(target)
			if !valid {
				err = fmt.Errorf("workload has no pod selector")
			} else {
				request, cancel := context.WithTimeout(ctx, 15*time.Second)
				pods, err = b.List(request, Query{Resource: r, Namespace: target.GetNamespace(), Labels: selector})
				cancel()
			}
		}
		if err != nil {
			if lastError != err.Error() {
				send("[kqt] Unable to discover log sources: " + err.Error())
				lastError = err.Error()
			}
			if target.GetKind() == "Pod" {
				for key, cancel := range active {
					cancel()
					delete(active, key)
				}
			}
			return
		}
		lastError = ""
		slices.SortFunc(pods, func(a, b unstructured.Unstructured) int { return strings.Compare(a.GetName(), b.GetName()) })
		wanted := map[string]logSource{}
		for _, pod := range pods {
			if pod.GetDeletionTimestamp() != nil {
				continue
			}
			containers := []string{container}
			if container == "*" {
				containers = Containers(pod)
			}
			for _, name := range containers {
				s := logSource{string(pod.GetUID()), pod.GetNamespace(), pod.GetName(), name}
				wanted[s.key()] = s
			}
		}
		for key, cancel := range active {
			if _, ok := wanted[key]; !ok {
				cancel()
				delete(active, key)
			}
		}
		// Prevent a broad selector from creating unbounded connections.
		keys := make([]string, 0, len(wanted))
		for key := range wanted {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		limited := false
		for _, key := range keys {
			if _, ok := active[key]; ok {
				continue
			}
			if len(active) >= 64 {
				limited = true
				continue
			}
			child, cancel := context.WithCancel(ctx)
			active[key] = cancel
			source := wanted[key]
			workers.Add(1)
			go func() {
				defer workers.Done()
				followSource(child, b, stream, source, options, send, opened...)
			}()
		}
		if limited && !limitedBefore {
			send("[kqt] Live logs limited to 64 containers; narrow the workload or choose a container.")
		}
		limitedBefore = limited
	}
	reconcile()
	if terminalError != nil {
		return terminalError
	}
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			reconcile()
			if terminalError != nil {
				return terminalError
			}
		}
	}
}

func followSource(ctx context.Context, b Backend, stream LogStreamer, source logSource, options LogOptions, emit func(string), opened ...func()) {
	var last *time.Time
	repeats := 0
	previousError := ""
	verified := true
	for ctx.Err() == nil {
		if reader, ok := b.(Editable); ok && !verified {
			resource, _ := Resolve(Builtins(), "pods")
			request, cancel := context.WithTimeout(ctx, 15*time.Second)
			pod, err := reader.Get(request, resource, source.ns, source.pod)
			cancel()
			if err == nil && string(pod.GetUID()) != source.uid {
				emit(source.prefix() + "Pod was replaced; this source has stopped.")
				return
			}
			if err != nil {
				if err.Error() != previousError {
					emit(source.prefix() + "Unable to check pod identity: " + err.Error())
					previousError = err.Error()
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
				continue
			}
		}
		verified = false
		reader, err := stream.StreamLogs(ctx, source.ns, source.pod, source.container, last, options)
		if err == nil {
			for _, ready := range opened {
				ready()
			}
			// A reconnect requests the last timestamp inclusively. Skip only the lines
			// already delivered at that timestamp, preserving distinct equal-time lines.
			skip, checkpoint := repeats, last
			buffer := bufio.NewReader(reader)
			for {
				line, truncated, readErr := readLogLine(buffer)
				if readErr != nil && line == "" {
					err = readErr
					break
				}
				if truncated {
					line += " … line truncated (1 MiB)"
				}
				stamp, parseErr := time.Parse(time.RFC3339Nano, strings.SplitN(line, " ", 2)[0])
				if parseErr == nil {
					if checkpoint != nil && (stamp.Before(*checkpoint) || stamp.Equal(*checkpoint) && skip > 0) {
						if stamp.Equal(*checkpoint) {
							skip--
						}
						continue
					}
					if last != nil && stamp.Equal(*last) {
						repeats++
					} else {
						value := stamp
						last = &value
						repeats = 1
					}
				}
				previousError = ""
				emit(source.prefix() + line)
				if readErr != nil {
					err = readErr
					break
				}
			}
			if err == io.EOF {
				err = nil
			}
			reader.Close()
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil && err.Error() != previousError {
			emit(source.prefix() + "Unable to follow logs: " + err.Error())
			previousError = err.Error()
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
