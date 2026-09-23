package graphstore

import (
	"safepatch/internal/domain/edition"
	"safepatch/internal/graph"
	"testing"
)

func TestChangeGraphRejectsForeignSQLRecordsAcrossEditions(t *testing.T) {
	for _, a := range edition.Editions() {
		for _, b := range edition.Editions() {
			t.Run(a.Name+"_to_"+b.Name, func(t *testing.T) {
				scope, node, _, _ := changeFixture(t)
				scope.Access.TenantID = "a_" + string(a.Key)
				f, _ := node.Fields()
				f.TenantID = "b_" + string(b.Key)
				foreign, err := graph.NewChangeNode(f)
				if err != nil {
					t.Fatal(err)
				}
				step := changeReadStep(scope, foreign)
				step.args[0] = scope.Access.TenantID
				store, _ := scriptStore(t, []sqlStep{step})
				if _, err := store.GetChangeNode(t.Context(), scope, f.Ref); err == nil {
					t.Fatal("poisoned tenant row returned")
				}
				// Wrong-tenant writes must not even begin a SQL transaction.
				store, _ = scriptStore(t, nil)
				if err := store.PutChangeNode(t.Context(), scope, foreign); err == nil {
					t.Fatal("foreign graph write")
				}
			})
		}
	}
}
