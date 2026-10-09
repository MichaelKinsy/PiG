package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// nodeRoom is one node's room for the benchmark pod: allocatable minus the requests of the pods bound to it, which
// is what the scheduler counts. It is not load: a node's actual use is unknown without a metrics API.
type nodeRoom struct {
	Name     string  `json:"name"`
	FreeCPU  float64 `json:"freeCpu"`
	FreeMem  float64 `json:"freeMemory"`
	AllocCPU float64 `json:"allocatableCpu"`
	Pods     int     `json:"pods"`
	// Skip says why the node cannot take the pod; empty when it can.
	Skip string `json:"skip,omitempty"`
}

type k8sNode struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Unschedulable bool `json:"unschedulable"`
		Taints        []struct {
			Key, Value, Effect string
		} `json:"taints"`
	} `json:"spec"`
	Status struct {
		Allocatable map[string]string `json:"allocatable"`
		Conditions  []struct{ Type, Status string }
	} `json:"status"`
}

type k8sResources struct {
	Requests map[string]string `json:"requests"`
}

type k8sContainer struct {
	Resources     k8sResources `json:"resources"`
	RestartPolicy string       `json:"restartPolicy"`
}

type k8sPod struct {
	Spec struct {
		NodeName       string            `json:"nodeName"`
		Containers     []k8sContainer    `json:"containers"`
		InitContainers []k8sContainer    `json:"initContainers"`
		Overhead       map[string]string `json:"overhead"`
		Resources      *k8sResources     `json:"resources"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

var quantityPattern = regexp.MustCompile(`^([+-]?[0-9.]+(?:[eE][+-]?[0-9]+)?)(m|k|M|G|T|P|E|Ki|Mi|Gi|Ti|Pi|Ei)?$`)

// parseQuantity reads a Kubernetes quantity in base units: cores for CPU, bytes for memory.
func parseQuantity(s string) (float64, error) {
	m := quantityPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("quantity %q", s)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("quantity %q: %w", s, err)
	}
	scale := map[string]float64{"": 1, "m": 1e-3, "k": 1e3, "M": 1e6, "G": 1e9, "T": 1e12, "P": 1e15, "E": 1e18,
		"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40, "Pi": 1 << 50, "Ei": 1 << 60}[m[2]]
	return v * scale, nil
}

// podRequests is what the scheduler reserves for a pod, as Kubernetes' PodRequests computes it: the containers'
// summed requests plus those of the sidecars (init containers with restartPolicy Always), or when larger an init
// container's plus the sidecars started before it; pod-level requests replace that aggregate; then the overhead.
func podRequests(p k8sPod, resource string) float64 {
	q := func(m map[string]string) float64 {
		v, _ := parseQuantity(m[resource])
		return v
	}
	var sum, sidecars, initMax float64
	for _, c := range p.Spec.Containers {
		sum += q(c.Resources.Requests)
	}
	for _, c := range p.Spec.InitContainers {
		r := q(c.Resources.Requests)
		if c.RestartPolicy == "Always" {
			sum += r
			sidecars += r
			r = sidecars
		} else {
			r += sidecars
		}
		initMax = math.Max(initMax, r)
	}
	reqs := math.Max(sum, initMax)
	if p.Spec.Resources != nil {
		if _, ok := p.Spec.Resources.Requests[resource]; ok {
			reqs = q(p.Spec.Resources.Requests)
		}
	}
	return reqs + q(p.Spec.Overhead)
}

// tolerates reports whether the tolerations admit a NoSchedule or NoExecute taint.
func tolerates(tolerations []toleration, key, value, effect string) bool {
	for _, t := range tolerations {
		if t.Effect != "" && t.Effect != effect {
			continue
		}
		if t.Operator == "Exists" && (t.Key == "" || t.Key == key) {
			return true
		}
		if (t.Operator == "" || t.Operator == "Equal") && t.Key == key && t.Value == value {
			return true
		}
	}
	return false
}

// rankNodes ranks the nodes that can take the pod (Ready, schedulable, matching the node selector, tolerating its
// taints, with room for the pod's CPU and memory) by free CPU, then free memory, then name; the others follow with
// the reason they were skipped.
func rankNodes(nodes []k8sNode, pods []k8sPod, v values) ([]nodeRoom, error) {
	wantCPU := float64(v.CPUs)
	wantMem, err := parseQuantity(v.Memory)
	if err != nil {
		return nil, err
	}
	usedCPU, usedMem, count := map[string]float64{}, map[string]float64{}, map[string]int{}
	for _, p := range pods {
		if p.Spec.NodeName == "" || p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed" {
			continue
		}
		usedCPU[p.Spec.NodeName] += podRequests(p, "cpu")
		usedMem[p.Spec.NodeName] += podRequests(p, "memory")
		count[p.Spec.NodeName]++
	}
	var rooms []nodeRoom
	for _, n := range nodes {
		name := n.Metadata.Name
		allocCPU, _ := parseQuantity(n.Status.Allocatable["cpu"])
		allocMem, _ := parseQuantity(n.Status.Allocatable["memory"])
		r := nodeRoom{Name: name, AllocCPU: allocCPU, FreeCPU: allocCPU - usedCPU[name], FreeMem: allocMem - usedMem[name], Pods: count[name]}
		ready := false
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
			}
		}
		switch {
		case !ready:
			r.Skip = "not Ready"
		case n.Spec.Unschedulable:
			r.Skip = "cordoned"
		case r.FreeCPU < wantCPU:
			r.Skip = fmt.Sprintf("%.2f CPUs free, the pod needs %d", r.FreeCPU, v.CPUs)
		case r.FreeMem < wantMem:
			r.Skip = fmt.Sprintf("%.1f GiB free, the pod needs %s", r.FreeMem/(1<<30), v.Memory)
		}
		for k, val := range v.NodeSelector {
			if r.Skip == "" && n.Metadata.Labels[k] != val {
				r.Skip = "does not match the node selector"
			}
		}
		for _, t := range n.Spec.Taints {
			if r.Skip == "" && (t.Effect == "NoSchedule" || t.Effect == "NoExecute") && !tolerates(v.Tolerations, t.Key, t.Value, t.Effect) {
				r.Skip = "taint " + t.Key + ":" + t.Effect + " is not tolerated"
			}
		}
		rooms = append(rooms, r)
	}
	slices.SortStableFunc(rooms, func(a, b nodeRoom) int {
		switch {
		case (a.Skip == "") != (b.Skip == ""):
			if a.Skip == "" {
				return -1
			}
			return 1
		case a.FreeCPU != b.FreeCPU:
			if a.FreeCPU > b.FreeCPU {
				return -1
			}
			return 1
		case a.FreeMem != b.FreeMem:
			if a.FreeMem > b.FreeMem {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return rooms, nil
}

// Ranking bases: what a node ranking could read.
const (
	// basisCluster: every node and every unfinished pod in every namespace, which is what the scheduler counts.
	basisCluster = "cluster"
	// basisNamespace: every node, but only the session namespace's pods; free CPU is an upper bound.
	basisNamespace = "namespace"
	// basisCandidates: no node objects, only the -nodes candidates and the session namespace's pods; nodes rank by
	// the CPU our own pods request on them, then in the order given.
	basisCandidates = "candidates"
)

// nodeRanking is a ranking and what it was based on; nodes.json records it.
type nodeRanking struct {
	Basis string     `json:"basis"`
	Nodes []nodeRoom `json:"nodes"`
}

// pickNodes picks the n nodes with the most free CPU for the pod, from candidates when there are any. It reads as
// much as the account may: every node and every pod; when pods in other namespaces are forbidden, the session
// namespace's pods; when nodes are forbidden too, only the candidates, which must then be given. Only a Forbidden
// answer narrows the ranking; any other failure ends the pick, so a lost connection or a timeout never passes for a
// missing permission.
func pickNodes(ctx context.Context, k kubectl, v values, n int, candidates []string, progress io.Writer) ([]string, nodeRanking, error) {
	var ranking nodeRanking
	unfinished := []string{"-o", "json", "--field-selector", "status.phase!=Succeeded,status.phase!=Failed"}
	var pods struct{ Items []k8sPod }
	nodeData, nodeErr := k.run(ctx, nil, "get", "nodes", "-o", "json")
	podData, podErr := k.run(ctx, nil, append([]string{"get", "pods", "--all-namespaces"}, unfinished...)...)
	ranking.Basis = basisCluster
	if podErr != nil {
		if !forbidden(podErr) {
			return nil, ranking, podErr
		}
		var err error
		if podData, err = k.run(ctx, nil, append([]string{"get", "pods"}, unfinished...)...); err != nil {
			return nil, ranking, fmt.Errorf("picking a node needs list on pods, at least in the namespace: %w", err)
		}
		ranking.Basis = basisNamespace
		_, _ = fmt.Fprintf(progress, "k8sbench: pods in other namespaces are not listable; the ranking counts only the requests of namespace %s, so free CPU is an upper bound\n", v.Namespace)
	}
	if err := json.Unmarshal(podData, &pods); err != nil {
		return nil, ranking, fmt.Errorf("pods: %w", err)
	}
	if nodeErr != nil {
		if !forbidden(nodeErr) {
			return nil, ranking, nodeErr
		}
		if len(candidates) == 0 {
			return nil, ranking, fmt.Errorf("nodes are not listable, so there is nothing to rank; name the candidates with -nodes a,b,c (with -spread N or -pick-node to choose among them): %w", nodeErr)
		}
		ranking.Basis = basisCandidates
		ranking.Nodes = rankCandidates(candidates, pods.Items)
		_, _ = fmt.Fprintf(progress, "k8sbench: nodes are not listable; ranking the -nodes candidates by the CPU namespace %s requests on them, then in the order given\n", v.Namespace)
		var picked []string
		for _, r := range ranking.Nodes {
			mark := " "
			if len(picked) < n {
				picked = append(picked, r.Name)
				mark = "*"
			}
			_, _ = fmt.Fprintf(progress, "k8sbench: %s %-40s allocatable unknown, %6.2f CPUs requested by namespace %s, %3d pods\n", mark, r.Name, -r.FreeCPU, v.Namespace, r.Pods)
		}
		return picked, ranking, nil
	}
	var nodes struct{ Items []k8sNode }
	if err := json.Unmarshal(nodeData, &nodes); err != nil {
		return nil, ranking, fmt.Errorf("nodes: %w", err)
	}
	if len(candidates) > 0 {
		var kept []k8sNode
		for _, c := range candidates {
			i := slices.IndexFunc(nodes.Items, func(n k8sNode) bool { return n.Metadata.Name == c })
			if i < 0 {
				return nil, ranking, fmt.Errorf("-nodes names %s, which is not a node of the cluster", c)
			}
			kept = append(kept, nodes.Items[i])
		}
		nodes.Items = kept
	}
	rooms, err := rankNodes(nodes.Items, pods.Items, v)
	if err != nil {
		return nil, ranking, err
	}
	ranking.Nodes = rooms
	var picked []string
	for _, r := range rooms {
		mark := " "
		if r.Skip == "" && len(picked) < n {
			picked = append(picked, r.Name)
			mark = "*"
		}
		_, _ = fmt.Fprintf(progress, "k8sbench: %s %-40s %6.2f of %6.2f CPUs free, %7.1f GiB free, %3d pods %s\n", mark, r.Name, r.FreeCPU, r.AllocCPU, r.FreeMem/(1<<30), r.Pods, r.Skip)
	}
	if len(picked) < n {
		return nil, ranking, fmt.Errorf("%d nodes can take the pod, %d wanted", len(picked), n)
	}
	return picked, ranking, nil
}

// forbidden reports whether kubectl failed because the API server refused the account: kubectl prints a Forbidden
// status as "Error from server (Forbidden): ...".
func forbidden(err error) bool {
	//portlint:allow erroridentity kubectl is a subprocess; its printed API status is the only identity the error carries
	return err != nil && strings.Contains(err.Error(), "Error from server (Forbidden)")
}

// rankCandidates ranks named nodes whose objects are unreadable by the CPU the listed pods request on them (least
// first; FreeCPU holds its negation), then in the given order.
func rankCandidates(candidates []string, pods []k8sPod) []nodeRoom {
	var rooms []nodeRoom
	for _, c := range candidates {
		r := nodeRoom{Name: c}
		for _, p := range pods {
			if p.Spec.NodeName == c && p.Status.Phase != "Succeeded" && p.Status.Phase != "Failed" {
				r.FreeCPU -= podRequests(p, "cpu")
				r.FreeMem -= podRequests(p, "memory")
				r.Pods++
			}
		}
		rooms = append(rooms, r)
	}
	slices.SortStableFunc(rooms, func(a, b nodeRoom) int {
		switch {
		case a.FreeCPU > b.FreeCPU:
			return -1
		case a.FreeCPU < b.FreeCPU:
			return 1
		}
		return 0
	})
	return rooms
}
