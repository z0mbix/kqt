package kube

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// Safe keeps server-controlled text from issuing terminal control sequences.
func Safe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u202e' || r == '\u202d' || r >= '\u2066' && r <= '\u2069' {
			return '�'
		}
		return r
	}, s)
}
func SafeLines(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = Safe(strings.ReplaceAll(lines[i], "\t", "    "))
	}
	return strings.Join(lines, "\n")
}
func Identity(o unstructured.Unstructured) string {
	if o.GetUID() != "" {
		return string(o.GetUID())
	}
	return o.GetNamespace() + "/" + o.GetName()
}
func Number(o unstructured.Unstructured, path ...string) int64 {
	n, _, _ := unstructured.NestedInt64(o.Object, path...)
	return n
}
func Text(o unstructured.Unstructured, path ...string) string {
	s, _, _ := unstructured.NestedString(o.Object, path...)
	return s
}
func Slice(o unstructured.Unstructured, path ...string) []interface{} {
	v, _, _ := unstructured.NestedSlice(o.Object, path...)
	return v
}
func readyCondition(o unstructured.Unstructured) bool {
	for _, v := range Slice(o, "status", "conditions") {
		c, ok := v.(map[string]interface{})
		if ok && c["type"] == "Ready" && c["status"] == "True" {
			return true
		}
	}
	return false
}
func Status(o unstructured.Unstructured) string {
	if o.GetDeletionTimestamp() != nil {
		return "Terminating"
	}
	if IsTableRow(o) {
		return tableStatus(o)
	}
	if o.GetKind() == "Pod" {
		for _, path := range []string{"initContainerStatuses", "containerStatuses"} {
			for _, value := range Slice(o, "status", path) {
				c, ok := value.(map[string]interface{})
				if !ok {
					continue
				}
				state, _ := c["state"].(map[string]interface{})
				waiting, _ := state["waiting"].(map[string]interface{})
				if reason, ok := waiting["reason"].(string); ok && reason != "" {
					return reason
				}
				terminated, _ := state["terminated"].(map[string]interface{})
				if code, _ := terminated["exitCode"].(int64); code != 0 {
					if reason, ok := terminated["reason"].(string); ok {
						return reason
					}
				}
			}
		}
		phase := Text(o, "status", "phase")
		if phase == "Running" && !readyCondition(o) {
			return "NotReady"
		}
		if phase != "" {
			return phase
		}
	}
	if o.GetKind() == "Node" {
		if readyCondition(o) {
			return "Ready"
		}
		return "NotReady"
	}
	if phase := Text(o, "status", "phase"); phase != "" {
		return phase
	}
	switch o.GetKind() {
	case "Deployment", "StatefulSet", "ReplicaSet":
		desired := Number(o, "spec", "replicas")
		if _, exists, _ := unstructured.NestedFieldNoCopy(o.Object, "spec", "replicas"); !exists {
			desired = 1
		}
		if Number(o, "status", "observedGeneration") < o.GetGeneration() {
			return "Progressing"
		}
		if Number(o, "status", "readyReplicas") >= desired {
			return "Ready"
		}
		return "Progressing"
	case "DaemonSet":
		if Number(o, "status", "numberReady") >= Number(o, "status", "desiredNumberScheduled") {
			return "Ready"
		}
		return "Progressing"
	case "Job":
		for _, v := range Slice(o, "status", "conditions") {
			c, ok := v.(map[string]interface{})
			if ok && c["status"] == "True" {
				if c["type"] == "Failed" {
					return "Failed"
				}
				if c["type"] == "Complete" {
					return "Succeeded"
				}
			}
		}
		if Number(o, "status", "active") > 0 {
			return "Running"
		}
		return "Pending"
	case "Namespace":
		return "Active"
	case "Event":
		return Text(o, "type")
	}
	for _, v := range Slice(o, "status", "conditions") {
		c, ok := v.(map[string]interface{})
		if ok && c["type"] == "Ready" {
			if c["status"] == "True" {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "—"
}
func Age(o unstructured.Unstructured) string {
	t := o.GetCreationTimestamp()
	if t.IsZero() {
		return "—"
	}
	d := max(time.Duration(0), time.Since(t.Time))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
func Column(o unstructured.Unstructured, id string) string {
	if o.GetKind() == "Event" {
		switch id {
		case "lastseen":
			if timestamp := EventTime(o); !timestamp.IsZero() {
				return timestamp.UTC().Format("2006-01-02 15:04:05")
			}
			return "—"
		case "object":
			return strings.Trim(EventSubject(o, "kind")+"/"+EventSubject(o, "name"), "/")
		case "count":
			return fmt.Sprint(EventCount(o))
		case "message":
			return EventMessage(o)
		}
	}
	if IsTableRow(o) {
		if value, ok := tableColumn(o, id); ok {
			return value
		}
	}
	switch id {
	case "name":
		return o.GetName()
	case "namespace":
		return o.GetNamespace()
	case "kind":
		return o.GetKind()
	case "status":
		return Status(o)
	case "age":
		return Age(o)
	case "node":
		return Text(o, "spec", "nodeName")
	case "ip":
		if o.GetKind() == "Service" {
			return Text(o, "spec", "clusterIP")
		}
		return Text(o, "status", "podIP")
	case "type":
		if o.GetKind() == "Event" {
			return Text(o, "type")
		}
		return Text(o, "spec", "type")
	case "reason":
		return Text(o, "reason")
	case "message":
		return Text(o, "message")
	case "restarts":
		var n int64
		for _, v := range Slice(o, "status", "containerStatuses") {
			c, ok := v.(map[string]interface{})
			if ok {
				count, _ := c["restartCount"].(int64)
				n += count
			}
		}
		return strconv.FormatInt(n, 10)
	case "ready":
		if o.GetKind() == "Pod" {
			n := 0
			for _, v := range Slice(o, "status", "containerStatuses") {
				c, ok := v.(map[string]interface{})
				if ok && c["ready"] == true {
					n++
				}
			}
			return fmt.Sprintf("%d/%d", n, len(Slice(o, "spec", "containers")))
		}
		if o.GetKind() == "DaemonSet" {
			return fmt.Sprintf("%d/%d", Number(o, "status", "numberReady"), Number(o, "status", "desiredNumberScheduled"))
		}
		n := Number(o, "spec", "replicas")
		if _, ok, _ := unstructured.NestedFieldNoCopy(o.Object, "spec", "replicas"); !ok {
			n = 1
		}
		return fmt.Sprintf("%d/%d", Number(o, "status", "readyReplicas"), n)
	case "ports":
		var ports []string
		for _, v := range Slice(o, "spec", "ports") {
			p, ok := v.(map[string]interface{})
			if ok {
				ports = append(ports, fmt.Sprintf("%v/%v", p["port"], p["protocol"]))
			}
		}
		return strings.Join(ports, ",")
	case "labels":
		var pairs []string
		for k, v := range o.GetLabels() {
			pairs = append(pairs, k+"="+v)
		}
		sort.Strings(pairs)
		return strings.Join(pairs, ",")
	}
	if strings.HasPrefix(id, "/") {
		var value interface{} = o.Object
		for _, part := range strings.Split(strings.TrimPrefix(id, "/"), "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			switch v := value.(type) {
			case map[string]interface{}:
				value = v[part]
			case []interface{}:
				i, e := strconv.Atoi(part)
				if e != nil || i < 0 || i >= len(v) {
					return "—"
				}
				value = v[i]
			default:
				return "—"
			}
		}
		if value == nil {
			return "—"
		}
		if s, ok := value.(string); ok {
			return s
		}
		data, _ := json.Marshal(value)
		return string(data)
	}
	return "—"
}
func DefaultColumns(kind string) []string {
	base := []string{"namespace", "name"}
	switch kind {
	case "Pod":
		return append(base, "ready", "status", "restarts", "age", "node")
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		return append(base, "ready", "status", "age")
	case "Service":
		return append(base, "type", "ip", "ports", "age")
	case "Event":
		return []string{"namespace", "lastseen", "type", "object", "reason", "count", "message"}
	case "Node", "Namespace", "PersistentVolume":
		return []string{"name", "status", "age"}
	default:
		return append(base, "status", "age")
	}
}

// Redacted also removes managedFields to keep inspection useful and compact.
func Redacted(o unstructured.Unstructured) unstructured.Unstructured {
	o = *o.DeepCopy()
	o.SetManagedFields(nil)
	if o.GetKind() == "Secret" {
		for _, key := range []string{"data", "stringData"} {
			if values, ok := o.Object[key].(map[string]interface{}); ok {
				for k := range values {
					values[k] = "<redacted>"
				}
			}
		}
		annotations := o.GetAnnotations()
		delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		o.SetAnnotations(annotations)
	}
	return o
}
func Manifest(o unstructured.Unstructured, asJSON bool) string {
	return manifestText(Redacted(o), asJSON)
}
func manifestText(o unstructured.Unstructured, asJSON bool) string {
	o = withoutTable(o)
	var data []byte
	var err error
	if asJSON {
		data, err = json.MarshalIndent(o.Object, "", "  ")
	} else {
		data, err = yaml.Marshal(o.Object)
	}
	if err != nil {
		return err.Error()
	}
	return SafeLines(string(data))
}
func Containers(o unstructured.Unstructured) []string {
	var names []string
	base := []string{"spec"}
	if o.GetKind() == "Deployment" || o.GetKind() == "StatefulSet" {
		base = []string{"spec", "template", "spec"}
	}
	for _, path := range []string{"containers", "initContainers", "ephemeralContainers"} {
		for _, v := range Slice(o, append(base, path)...) {
			c, ok := v.(map[string]interface{})
			if ok {
				if name, ok := c["name"].(string); ok {
					names = append(names, name)
				}
			}
		}
	}
	return names
}
func PodSelector(o unstructured.Unstructured) (labels, fields string, ok bool) {
	if o.GetKind() == "Node" {
		return "", "spec.nodeName=" + o.GetName(), true
	}
	if o.GetKind() == "Service" {
		selector, _, _ := unstructured.NestedStringMap(o.Object, "spec", "selector")
		if len(selector) == 0 {
			return "", "", false
		}
		return metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: selector}), "", true
	}
	if o.GetKind() == "Deployment" || o.GetKind() == "StatefulSet" || o.GetKind() == "DaemonSet" || o.GetKind() == "ReplicaSet" || o.GetKind() == "Job" {
		v, exists, _ := unstructured.NestedMap(o.Object, "spec", "selector")
		if !exists || len(v) == 0 {
			return "", "", false
		}
		data, _ := json.Marshal(v)
		var selector metav1.LabelSelector
		if json.Unmarshal(data, &selector) != nil {
			return "", "", false
		}
		parsed, err := metav1.LabelSelectorAsSelector(&selector)
		if err != nil || parsed.Empty() {
			return "", "", false
		}
		return parsed.String(), "", true
	}
	return "", "", false
}
