package workqueue_test

import (
	"encoding/json"
	"github.com/nats-io/nats.go"
	"safepatch/internal/domain/edition"
	"safepatch/internal/platform/workqueue"
	"testing"
)

func TestAllEditionPairsCannotRedirectBrokerWork(t *testing.T) {
	for _, a := range edition.Editions() {
		for _, b := range edition.Editions() {
			t.Run(a.Name+"_to_"+b.Name, func(t *testing.T) {
				for _, kind := range []string{workqueue.Proof, workqueue.GitHubPR} {
					orgA, orgB := "a_"+string(a.Key), "b_"+string(b.Key)
					msg, err := workqueue.Encode(kind, orgA, "same_id", "WORK")
					if err != nil {
						t.Fatal(err)
					}
					if own, err := workqueue.Decode(msg, kind); err != nil || own.OrganizationID != orgA {
						t.Fatal(own, err)
					}
					foreign, _ := workqueue.Encode(kind, orgB, "same_id", "WORK")
					if msg.Header.Get("Nats-Msg-Id") == foreign.Header.Get("Nats-Msg-Id") {
						t.Fatal("cross tenant dedup collision")
					}
					msg.Data = foreign.Data
					if _, err := workqueue.Decode(msg, kind); err == nil {
						t.Fatal("foreign envelope routed under attacker subject")
					}
					msg.Data, _ = json.Marshal(map[string]string{"organization_id": orgA, "work_id": "same_id", "actor": "victim"})
					if _, err := workqueue.Decode(msg, kind); err == nil {
						t.Fatal("actor injection")
					}
				}
			})
		}
	}
}
func TestQueueRejectsAmbiguityAndLegacyGlobalSubjects(t *testing.T) {
	for _, body := range []string{`{"organization_id":"a","organization_id":"b","work_id":"id"}`, `{"Organization_ID":"a","work_id":"id"}`, `{"organization_id":"a","work_id":null}`, `{"organization_id":"*","work_id":"id"}`, `{"organization_id":"a.b","work_id":"id"}`} {
		m := nats.NewMsg("safepatch.tenant.a.proof")
		m.Data = []byte(body)
		if _, err := workqueue.Decode(m, workqueue.Proof); err == nil {
			t.Fatal(body)
		}
	}
	m, _ := workqueue.Encode(workqueue.Proof, "org", "id", "WORK")
	m.Subject = "safepatch.proof.requested"
	if _, err := workqueue.Decode(m, workqueue.Proof); err == nil {
		t.Fatal("legacy global queue accepted")
	}
}
