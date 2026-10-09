package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// kubectl runs kubectl with the configured kubeconfig, context and namespace. The runner talks to the cluster only
// through it, so the Mac or CI host needs kubectl and credentials, nothing else.
type kubectl interface {
	run(ctx context.Context, stdin []byte, args ...string) ([]byte, error)
}

type execKubectl struct {
	bin        string
	kubeconfig string
	context    string
	namespace  string
}

func (k execKubectl) args(args []string) []string {
	var global []string
	if k.kubeconfig != "" {
		global = append(global, "--kubeconfig", k.kubeconfig)
	}
	if k.context != "" {
		global = append(global, "--context", k.context)
	}
	if k.namespace != "" {
		global = append(global, "--namespace", k.namespace)
	}
	return append(global, args...)
}

func (k execKubectl) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, k.bin, k.args(args)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// jobOutcome is what the runner learns about a finished Job.
type jobOutcome struct {
	succeeded bool
	node      string
	pod       string
	log       []byte
	reason    string
}

// runJob applies the manifest, waits for the Job to finish, and collects the pod's node and log. Progress goes to
// progress; the Job is deleted afterwards unless keep is set. deliver, when set, runs once when the pod is running
// (bundle mode copies the artifacts then); its failure ends the run.
func runJob(ctx context.Context, k kubectl, manifest []byte, name string, poll, timeout time.Duration, keep bool, progress io.Writer, now func() time.Time, deliver func(context.Context, string) error) (jobOutcome, error) {
	var out jobOutcome
	if _, err := k.run(ctx, manifest, "apply", "-f", "-"); err != nil {
		return out, err
	}
	if !keep {
		defer func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			if _, err := k.run(ctx, nil, "delete", "job", name, "--ignore-not-found", "--wait=false", "--cascade=background"); err != nil {
				_, _ = fmt.Fprintf(progress, "k8sbench: could not delete job %s: %v\n", name, err)
			}
		}()
	}
	deadline := now().Add(timeout)
	lastPhase := ""
	for {
		if err := sleepCtx(ctx, poll); err != nil {
			return out, err
		}
		pod, phase, node, waiting, err := podStatus(ctx, k, name)
		if err != nil {
			return out, err
		}
		out.pod, out.node = pod, node
		if status := strings.TrimSpace(phase + " " + waiting); status != lastPhase {
			_, _ = fmt.Fprintf(progress, "k8sbench: %s pod %s %s %s\n", now().UTC().Format(time.RFC3339), pod, status, node)
			lastPhase = status
		}
		if deliver != nil && phase == "Running" && pod != "" {
			if err := deliver(ctx, pod); err != nil {
				// A pod that already stopped (its own check failed) has the reason in its log: collect it below.
				if _, phase, _, _, perr := podStatus(ctx, k, name); perr != nil || phase == "Running" || phase == "Pending" {
					return out, fmt.Errorf("copy the bundle into pod %s: %w", pod, err)
				}
				_, _ = fmt.Fprintf(progress, "k8sbench: pod %s stopped before the bundle arrived\n", pod)
			}
			deliver = nil
		}
		done, ok, reason, err := jobStatus(ctx, k, name)
		if err != nil {
			return out, err
		}
		if done {
			out.succeeded, out.reason = ok, reason
			break
		}
		if now().After(deadline) {
			return out, fmt.Errorf("job %s did not finish within %s (pod %s %s)", name, timeout, phase, waiting)
		}
	}
	if out.pod == "" {
		return out, fmt.Errorf("job %s finished (%s) without a pod", name, out.reason)
	}
	log, err := k.run(ctx, nil, "logs", "pod/"+out.pod, "-c", "bench")
	if err != nil {
		return out, err
	}
	out.log = log
	return out, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// copyBundle copies the packed bundle into a running pod, then the READY marker the pod waits for; the pod checks the
// bundle's sha256 before it unpacks it.
func copyBundle(ctx context.Context, k kubectl, pod, bundleTar string, progress io.Writer, now func() time.Time) error {
	marker := filepath.Join(filepath.Dir(bundleTar), "READY")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		return err
	}
	started := now()
	if _, err := k.run(ctx, nil, "cp", bundleTar, pod+":"+bundleDir+"/bundle.tar.gz", "-c", "bench"); err != nil {
		return err
	}
	if _, err := k.run(ctx, nil, "cp", marker, pod+":"+bundleDir+"/READY", "-c", "bench"); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(progress, "k8sbench: copied the bundle into pod %s in %s\n", pod, now().Sub(started).Round(time.Millisecond))
	return nil
}

// jobStatus reads the Job's Complete or Failed condition.
func jobStatus(ctx context.Context, k kubectl, name string) (done, succeeded bool, reason string, err error) {
	data, err := k.run(ctx, nil, "get", "job", name, "-o", "json")
	if err != nil {
		return false, false, "", err
	}
	var job struct {
		Status struct {
			Conditions []struct{ Type, Status, Reason, Message string } `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(data, &job); err != nil {
		return false, false, "", fmt.Errorf("job %s: %w", name, err)
	}
	for _, c := range job.Status.Conditions {
		if c.Status != "True" {
			continue
		}
		switch c.Type {
		case "Complete":
			return true, true, c.Reason, nil
		case "Failed":
			return true, false, strings.TrimSpace(c.Reason + " " + c.Message), nil
		}
	}
	return false, false, "", nil
}

// podStatus finds the Job's pod and reports its phase, node and the reason a container is waiting (such as
// ErrImagePull), which is how an unschedulable or unpullable pod shows up before the deadline.
func podStatus(ctx context.Context, k kubectl, job string) (name, phase, node, waiting string, err error) {
	data, err := k.run(ctx, nil, "get", "pods", "-l", "job-name="+job, "-o", "json")
	if err != nil {
		return "", "", "", "", err
	}
	var list struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
			Spec     struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase      string `json:"phase"`
				Conditions []struct{ Type, Status, Reason, Message string }
				Containers []struct {
					State struct {
						Waiting *struct{ Reason string } `json:"waiting"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return "", "", "", "", err
	}
	if len(list.Items) == 0 {
		return "", "", "", "", nil
	}
	p := list.Items[len(list.Items)-1]
	for _, c := range p.Status.Containers {
		if c.State.Waiting != nil && c.State.Waiting.Reason != "" {
			waiting = c.State.Waiting.Reason
		}
	}
	for _, c := range p.Status.Conditions {
		if c.Type == "PodScheduled" && c.Status == "False" && c.Reason != "" {
			waiting = c.Reason
		}
	}
	return p.Metadata.Name, p.Status.Phase, p.Spec.NodeName, waiting, nil
}

// clusterFacts reads what the API server says about the node: its kernel, OS and kubelet from the Node object, and
// the CPU manager policy from the kubelet's configz. A namespace-scoped account usually may read neither; each
// unreadable fact is recorded as such.
func clusterFacts(ctx context.Context, k kubectl, node string) *object {
	o := newObject()
	o.set("node", node)
	if node == "" {
		return o
	}
	if data, err := k.run(ctx, nil, "get", "node", node, "-o", "json"); err == nil {
		// nodeInfo holds objects too (swap, features), so only its string fields are read.
		var n struct {
			Status struct {
				NodeInfo    map[string]any    `json:"nodeInfo"`
				Capacity    map[string]string `json:"capacity"`
				Allocatable map[string]string `json:"allocatable"`
			} `json:"status"`
		}
		if err := json.Unmarshal(data, &n); err != nil {
			o.set("nodeInfo", "undecodable: "+err.Error())
		} else {
			info := newObject()
			for _, key := range []string{"kernelVersion", "osImage", "architecture", "containerRuntimeVersion", "kubeletVersion"} {
				if v, ok := n.Status.NodeInfo[key].(string); ok {
					info.set(key, v)
				}
			}
			info.set("cpuCapacity", n.Status.Capacity["cpu"])
			info.set("cpuAllocatable", n.Status.Allocatable["cpu"])
			o.set("nodeInfo", info)
		}
	} else {
		o.set("nodeInfo", "unreadable")
	}
	if data, err := k.run(ctx, nil, "get", "--raw", "/api/v1/nodes/"+node+"/proxy/configz"); err == nil {
		var z struct {
			KubeletConfig struct {
				CPUManagerPolicy      string `json:"cpuManagerPolicy"`
				TopologyManagerPolicy string `json:"topologyManagerPolicy"`
			} `json:"kubeletconfig"`
		}
		if json.Unmarshal(data, &z) == nil {
			policy := z.KubeletConfig.CPUManagerPolicy
			if policy == "" {
				policy = "none"
			}
			o.set("cpuManagerPolicy", policy)
			if z.KubeletConfig.TopologyManagerPolicy != "" {
				o.set("topologyManagerPolicy", z.KubeletConfig.TopologyManagerPolicy)
			}
		}
	} else {
		o.set("cpuManagerPolicy", "unreadable")
	}
	return o
}
