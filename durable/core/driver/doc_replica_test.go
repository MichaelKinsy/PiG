package driver

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

func replicaValue(t *testing.T, text string) durable.JsonObject {
	t.Helper()
	value, err := delta.DecodeJson([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	object, err := jsonObject(value)
	if err != nil {
		t.Fatal(err)
	}
	return object
}

func TestDocReplicaCatchesUpWithTheOperationsTheCoreSent(t *testing.T) {
	var replicas docReplicas
	if _, err := replicas.resolve("doc", nil, docReading{value: replicaValue(t, `{"n":1}`), epoch: 4, adopted: 2, revised: true}); err != nil {
		t.Fatal(err)
	}
	held := replicas.held("doc")
	if got := revisions(held); got["epoch"] != uint64(4) || got["adopted"] != 2 {
		t.Fatalf("revisions %v", got)
	}
	ops := delta.Ops{{"s", delta.Path{"n"}, 2.0}}
	value, err := replicas.resolve("doc", held, docReading{incremental: true, ops: ops, epoch: 4, adopted: 3, revised: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Get("n"); got != 2.0 {
		t.Fatalf("value n = %v", got)
	}
	if now := replicas.held("doc"); now.adopted != 3 || now == held {
		t.Fatalf("the replica is %+v", now)
	}
	if first, _ := held.value.Get("n"); first != 1.0 {
		t.Fatal("the replica a request named changed under it")
	}
	if _, err := replicas.resolve("doc", held, docReading{incremental: true, epoch: 5, adopted: 4, revised: true}); err == nil {
		t.Fatal("operations relative to another epoch than the held one were applied")
	}
}

func TestDocReplicasKeepOnlyAnswersTheCoreNamedAndAFewDocuments(t *testing.T) {
	var replicas docReplicas
	if _, err := replicas.resolve("edited", nil, docReading{value: replicaValue(t, `{}`)}); err != nil || replicas.held("edited") != nil {
		t.Fatalf("an answer with no revision was kept (%v)", err)
	}
	for index := range maxDocReplicas + 1 {
		if _, err := replicas.resolve(fmt.Sprint("doc", index), nil, docReading{value: replicaValue(t, `{}`), epoch: 1, adopted: index, revised: true}); err != nil {
			t.Fatal(err)
		}
	}
	if replicas.held("doc0") != nil || replicas.held(fmt.Sprint("doc", maxDocReplicas)) == nil || len(replicas.byID) != maxDocReplicas {
		t.Fatalf("%d replicas kept, the oldest one %v", len(replicas.byID), replicas.held("doc0"))
	}
}
