package kube

import (
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
)

type TableMode string

const (
	TableOff      TableMode = ""
	TableNone     TableMode = "None"
	TableMetadata TableMode = "Metadata"
)

const (
	tableAccept = "application/json;as=Table;v=v1;g=meta.k8s.io,application/json"
	tableCells  = "kqt.table/cells"
)

type TableLister interface {
	ServerTables() bool
}

func (c *Client) ServerTables() bool { return true }

func IsTableRow(o unstructured.Unstructured) bool {
	_, ok := o.Object[tableCells]
	return ok
}

func tableCell(o unstructured.Unstructured, name string) (string, bool) {
	cells, _ := o.Object[tableCells].(map[string]interface{})
	value, ok := cells[name].(string)
	if value == "<none>" || value == "<unknown>" {
		value = ""
	}
	return value, ok
}

func withoutTable(o unstructured.Unstructured) unstructured.Unstructured {
	if !IsTableRow(o) {
		return o
	}
	copy := make(map[string]interface{}, len(o.Object))
	for k, v := range o.Object {
		if k != tableCells {
			copy[k] = v
		}
	}
	return unstructured.Unstructured{Object: copy}
}

func tablePath(r Resource, namespace string) string {
	path := "/apis/" + r.GVR.Group + "/" + r.GVR.Version
	if r.GVR.Group == "" {
		path = "/api/" + r.GVR.Version
	}
	if r.Namespaced && namespace != "" {
		path += "/namespaces/" + namespace
	}
	return path + "/" + r.GVR.Resource
}

func tableRequest(client rest.Interface, q Query, options metav1.ListOptions) *rest.Request {
	request := client.Get().AbsPath(tablePath(q.Resource, q.Namespace)).SetHeader("Accept", tableAccept).Param("includeObject", string(q.Table))
	if options.Limit > 0 {
		request.Param("limit", strconv.FormatInt(options.Limit, 10))
	}
	for name, value := range map[string]string{"continue": options.Continue, "labelSelector": options.LabelSelector, "fieldSelector": options.FieldSelector, "resourceVersion": options.ResourceVersion} {
		if value != "" {
			request.Param(name, value)
		}
	}
	if options.Watch {
		request.Param("watch", "true")
	}
	if options.AllowWatchBookmarks {
		request.Param("allowWatchBookmarks", "true")
	}
	if options.TimeoutSeconds != nil {
		request.Param("timeoutSeconds", strconv.FormatInt(*options.TimeoutSeconds, 10))
	}
	return request
}

type tableDecoder struct {
	resource  Resource
	namespace string
	columns   []metav1.TableColumnDefinition
}

func (d *tableDecoder) page(data []byte) ([]unstructured.Unstructured, string, string, error) {
	var head struct{ Kind string }
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, "", "", err
	}
	if head.Kind != "Table" {
		var list unstructured.UnstructuredList
		if err := list.UnmarshalJSON(data); err != nil {
			return nil, "", "", err
		}
		for i := range list.Items {
			d.complete(&list.Items[i])
		}
		return list.Items, list.GetResourceVersion(), list.GetContinue(), nil
	}
	var table metav1.Table
	if err := json.Unmarshal(data, &table); err != nil {
		return nil, "", "", err
	}
	if len(table.ColumnDefinitions) > 0 {
		d.columns = table.ColumnDefinitions
	}
	rows := make([]unstructured.Unstructured, 0, len(table.Rows))
	for _, row := range table.Rows {
		o, err := d.row(row, "")
		if err != nil {
			return nil, "", "", err
		}
		rows = append(rows, o)
	}
	return rows, table.ResourceVersion, table.Continue, nil
}

func (d *tableDecoder) complete(o *unstructured.Unstructured) {
	if o.GetKind() == "" {
		o.SetKind(d.resource.Kind)
	}
	if o.GetAPIVersion() == "" {
		o.SetAPIVersion(d.resource.GVR.GroupVersion().String())
	}
}

func (d *tableDecoder) row(row metav1.TableRow, version string) (unstructured.Unstructured, error) {
	cells := map[string]interface{}{}
	name := ""
	for i, column := range d.columns {
		if i >= len(row.Cells) {
			break
		}
		value := cellText(row.Cells[i])
		cells[strings.ToLower(column.Name)] = value
		if column.Format == "name" && name == "" {
			name = value
		}
	}
	metadata := map[string]interface{}{}
	if len(row.Object.Raw) > 0 {
		var partial struct {
			Metadata map[string]interface{} `json:"metadata"`
		}
		if err := json.Unmarshal(row.Object.Raw, &partial); err != nil {
			return unstructured.Unstructured{}, err
		}
		if partial.Metadata != nil {
			metadata = partial.Metadata
		}
		delete(metadata, "managedFields")
	}
	if metadata["name"] == nil {
		metadata["name"] = name
	}
	if metadata["namespace"] == nil && d.resource.Namespaced && d.namespace != "" {
		metadata["namespace"] = d.namespace
	}
	if metadata["resourceVersion"] == nil && version != "" {
		metadata["resourceVersion"] = version
	}
	if metadata["creationTimestamp"] == nil {
		if created, ok := tableCreated(d.columns, cells); ok {
			metadata["creationTimestamp"] = created.UTC().Format(time.RFC3339)
		}
	}
	o := unstructured.Unstructured{Object: map[string]interface{}{"metadata": metadata, tableCells: cells}}
	d.complete(&o)
	return o, nil
}

func cellText(cell interface{}) string {
	switch v := cell.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	data, _ := json.Marshal(cell)
	return string(data)
}

var humanDuration = regexp.MustCompile(`^(\d+[ydhms])+$`)
var durationPart = regexp.MustCompile(`(\d+)([ydhms])`)

func tableCreated(columns []metav1.TableColumnDefinition, cells map[string]interface{}) (time.Time, bool) {
	for _, column := range columns {
		value, _ := cells[strings.ToLower(column.Name)].(string)
		if column.Type == "date" {
			if created, err := time.Parse(time.RFC3339, value); err == nil {
				return created, true
			}
		}
		if strings.EqualFold(column.Name, "Age") || column.Type == "date" {
			if age, ok := parseAge(value); ok {
				return time.Now().Add(-age), true
			}
		}
	}
	return time.Time{}, false
}

func parseAge(value string) (time.Duration, bool) {
	if !humanDuration.MatchString(value) {
		return 0, false
	}
	units := map[string]time.Duration{"y": 365 * 24 * time.Hour, "d": 24 * time.Hour, "h": time.Hour, "m": time.Minute, "s": time.Second}
	var total time.Duration
	for _, part := range durationPart.FindAllStringSubmatch(value, -1) {
		n, _ := strconv.Atoi(part[1])
		total += time.Duration(n) * units[part[2]]
	}
	return total, true
}

func (d *tableDecoder) event(data []byte) (runtime.Object, error) {
	var head struct {
		Kind     string `json:"kind"`
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, err
	}
	if head.Kind != "Table" {
		o := &unstructured.Unstructured{}
		if err := o.UnmarshalJSON(data); err != nil {
			return nil, err
		}
		d.complete(o)
		return o, nil
	}
	var table metav1.Table
	if err := json.Unmarshal(data, &table); err != nil {
		return nil, err
	}
	if len(table.ColumnDefinitions) > 0 {
		d.columns = table.ColumnDefinitions
	}
	if len(table.Rows) == 0 {
		o := &unstructured.Unstructured{Object: map[string]interface{}{"metadata": map[string]interface{}{"resourceVersion": head.Metadata.ResourceVersion}}}
		d.complete(o)
		return o, nil
	}
	o, err := d.row(table.Rows[0], head.Metadata.ResourceVersion)
	return &o, err
}

type tableWatchDecoder struct {
	body    io.ReadCloser
	decoder *json.Decoder
	table   *tableDecoder
}

func (d *tableWatchDecoder) Decode() (watch.EventType, runtime.Object, error) {
	var event metav1.WatchEvent
	if err := d.decoder.Decode(&event); err != nil {
		return "", nil, err
	}
	kind := watch.EventType(event.Type)
	if kind == watch.Error {
		status := &metav1.Status{}
		if err := json.Unmarshal(event.Object.Raw, status); err != nil {
			return "", nil, err
		}
		return kind, status, nil
	}
	o, err := d.table.event(event.Object.Raw)
	return kind, o, err
}

func (d *tableWatchDecoder) Close() { d.body.Close() }

func (c *Client) tablePage(ctx context.Context, q Query, options metav1.ListOptions, decoder *tableDecoder) ([]unstructured.Unstructured, string, string, error) {
	data, err := tableRequest(c.core.RESTClient(), q, options).Do(ctx).Raw()
	if err != nil {
		return nil, "", "", err
	}
	return decoder.page(data)
}

func (c *Client) tableWatch(ctx context.Context, q Query, options metav1.ListOptions, decoder *tableDecoder) (watch.Interface, error) {
	options.Watch = true
	body, err := tableRequest(c.streamCore.RESTClient(), q, options).Stream(ctx)
	if err != nil {
		return nil, err
	}
	return watch.NewStreamWatcher(&tableWatchDecoder{body, json.NewDecoder(body), decoder}, apierrors.NewClientErrorReporter(500, "GET", "ClientWatchDecoding")), nil
}

func readyCounts(value string) (int, int, bool) {
	ready, desired, found := strings.Cut(value, "/")
	if !found {
		return 0, 0, false
	}
	a, errA := strconv.Atoi(ready)
	b, errB := strconv.Atoi(desired)
	return a, b, errA == nil && errB == nil
}

func numbersReady(o unstructured.Unstructured, ready, desired string) bool {
	r, _ := tableCell(o, ready)
	d, _ := tableCell(o, desired)
	a, errA := strconv.Atoi(r)
	b, errB := strconv.Atoi(d)
	return errA == nil && errB == nil && a >= b
}

func tableStatus(o unstructured.Unstructured) string {
	status, hasStatus := tableCell(o, "status")
	switch o.GetKind() {
	case "Pod":
		if status == "Completed" {
			return "Succeeded"
		}
		if ready, total, ok := readyCounts(cellOr(o, "ready")); status == "Running" && ok && ready < total {
			return "NotReady"
		}
	case "Node":
		first, _, _ := strings.Cut(status, ",")
		if first == "Ready" {
			return "Ready"
		}
		return "NotReady"
	case "Deployment", "StatefulSet":
		if ready, total, ok := readyCounts(cellOr(o, "ready")); ok && ready >= total {
			return "Ready"
		}
		return "Progressing"
	case "ReplicaSet", "DaemonSet":
		if numbersReady(o, "ready", "desired") {
			return "Ready"
		}
		return "Progressing"
	case "Job":
		switch status {
		case "Complete":
			return "Succeeded"
		case "Failed", "Running":
			return status
		case "":
			if done, total, ok := readyCounts(cellOr(o, "completions")); ok && done >= total {
				return "Succeeded"
			}
			return "Running"
		}
		return "Pending"
	case "Namespace":
		if status == "" {
			return "Active"
		}
	}
	if hasStatus && status != "" {
		return status
	}
	if ready, ok := tableCell(o, "ready"); ok {
		switch ready {
		case "True":
			return "Ready"
		case "False", "Unknown":
			return "NotReady"
		}
	}
	return "—"
}

func cellOr(o unstructured.Unstructured, name string) string {
	value, _ := tableCell(o, name)
	return value
}

func tableColumn(o unstructured.Unstructured, id string) (string, bool) {
	switch id {
	case "ready":
		switch o.GetKind() {
		case "DaemonSet", "ReplicaSet":
			return cellOr(o, "ready") + "/" + cellOr(o, "desired"), true
		}
		if value, ok := tableCell(o, "ready"); ok {
			return value, true
		}
		return "—", true
	case "restarts":
		count, _, _ := strings.Cut(cellOr(o, "restarts"), " ")
		if count == "" {
			count = "0"
		}
		return count, true
	case "node":
		return cellOr(o, "node"), true
	case "ip":
		if o.GetKind() == "Service" {
			return cellOr(o, "cluster-ip"), true
		}
		return cellOr(o, "ip"), true
	case "type":
		return cellOr(o, "type"), true
	case "ports":
		return cellOr(o, "port(s)"), true
	}
	if strings.HasPrefix(id, "/") {
		return "—", true
	}
	return "", false
}

var TableColumns = []string{"name", "namespace", "kind", "status", "age", "ready", "restarts", "node", "ip", "type", "ports", "labels", "cpu", "memory"}
