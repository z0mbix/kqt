package kube

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
)

// NamespaceNames parses an explicit namespace selection. Only the empty string
// means all namespaces; an empty member or wildcard never broadens a selection.
func NamespaceNames(scope string) ([]string, error) {
	if scope == "" {
		return nil, nil
	}
	var names []string
	seen := map[string]bool{}
	for _, part := range strings.Split(scope, ",") {
		name := strings.TrimSpace(part)
		if name == "" || len(validation.IsDNS1123Label(name)) != 0 {
			return nil, fmt.Errorf("invalid namespace %q", name)
		}
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	if len(names) > 64 {
		return nil, fmt.Errorf("select at most 64 namespaces")
	}
	return names, nil
}

func NormalizeNamespaces(scope string) (string, error) {
	names, err := NamespaceNames(scope)
	return strings.Join(names, ","), err
}

func NamespaceIncluded(scope, namespace string) bool {
	names, err := NamespaceNames(scope)
	if err != nil {
		return false
	}
	if len(names) == 0 {
		return true
	}
	for _, name := range names {
		if name == namespace {
			return true
		}
	}
	return false
}

type NamespaceErrors struct {
	Total, Available int
	Failures         map[string]error
}

func (e *NamespaceErrors) Error() string {
	var details []string
	for _, name := range slices.Sorted(maps.Keys(e.Failures)) {
		details = append(details, name+": "+Safe(e.Failures[name].Error()))
	}
	return fmt.Sprintf("Partial namespace results (%d/%d available): %s", e.Available, e.Total, strings.Join(details, "; "))
}

func (e *NamespaceErrors) Unwrap() []error {
	var result []error
	for _, name := range slices.Sorted(maps.Keys(e.Failures)) {
		result = append(result, e.Failures[name])
	}
	return result
}

func PartialNamespaces(err error) bool {
	var scope *NamespaceErrors
	return errors.As(err, &scope) && scope.Available > 0
}

// Bound concurrent requests while preserving deterministic namespace order.
func eachNamespace(ctx context.Context, names []string, work func(int, string) error) error {
	errorsByIndex := make([]error, len(names))
	slots := make(chan struct{}, 4)
	var tasks sync.WaitGroup
	for i, name := range names {
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
				errorsByIndex[i] = work(i, name)
			case <-ctx.Done():
				errorsByIndex[i] = ctx.Err()
			}
		}()
	}
	tasks.Wait()
	result := &NamespaceErrors{Total: len(names), Failures: map[string]error{}}
	for i, err := range errorsByIndex {
		if err == nil {
			result.Available++
		} else {
			result.Failures[names[i]] = err
		}
	}
	if len(result.Failures) == 0 {
		return nil
	}
	return result
}

func listNamespaces(ctx context.Context, q Query, names []string, list func(context.Context, Query) ([]unstructured.Unstructured, error)) ([]unstructured.Unstructured, error) {
	results := make([][]unstructured.Unstructured, len(names))
	err := eachNamespace(ctx, names, func(i int, name string) error {
		query := q
		query.Namespace = name
		objects, err := list(ctx, query)
		if err == nil {
			for _, object := range objects {
				if object.GetNamespace() == name {
					results[i] = append(results[i], object)
				}
			}
		}
		return err
	})
	var rows []unstructured.Unstructured
	for _, result := range results {
		rows = append(rows, result...)
	}
	return rows, err
}

func metricsNamespaces(ctx context.Context, kind string, names []string, reader MetricsReader) (map[string]Usage, error) {
	results := make([]map[string]Usage, len(names))
	err := eachNamespace(ctx, names, func(i int, name string) error {
		var err error
		results[i], err = reader.Metrics(ctx, kind, name)
		if err != nil {
			results[i] = nil
		}
		return err
	})
	usage := map[string]Usage{}
	for i, result := range results {
		for key, value := range result {
			if strings.HasPrefix(key, names[i]+"/") {
				usage[key] = value
			}
		}
	}
	return usage, err
}
