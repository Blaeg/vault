package vault

import (
	"reflect"
	"testing"

	"github.com/hashicorp/vault/helper/token"
)

func TestTokenStore_MemDBIndexes(t *testing.T) {
	var err error
	_, ts, _, _ := TestCoreWithTokenStore(t)

	tm1 := &token.TokenMapping{
		ID:       "testid",
		TokenID:  "testtokenid",
		Accessor: "testaccessor",
		ParentID: "testparentid",
	}
	err = ts.UpsertTokenMapping(tm1, true)
	if err != nil {
		t.Fatal(err)
	}

	tm2 := &token.TokenMapping{
		ID:       "testid2",
		TokenID:  "testtokenid2",
		Accessor: "testaccessor2",
		// Use the same parent for both the mappings
		ParentID: "testparentid",
	}
	err = ts.UpsertTokenMapping(tm2, true)
	if err != nil {
		t.Fatal(err)
	}

	tmFetched, err := ts.MemDBTokenMappingByTokenID("testtokenid", false)
	if err != nil {
		t.Fatal(err)
	}

	if tmFetched != tm1 {
		t.Fatalf("bad: same reference expected")
	}

	if !reflect.DeepEqual(tm1, tmFetched) {
		t.Fatalf("bad: token mapping; expected: %#v\n actual: %#v", tm1, tmFetched)
	}

	tmFetched, err = ts.MemDBTokenMappingByAccessor("testaccessor", false)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(tm1, tmFetched) {
		t.Fatalf("bad: token mapping; expected: %#v\n actual: %#v", tm1, tmFetched)
	}

	tmsFetched, err := ts.MemDBTokenMappingsByParentID("testparentid", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmsFetched) != 2 {
		t.Fatalf("bad: length of mappings; expected: 2, actual: %d", len(tmsFetched))
	}
	tm1Found := false
	tm2Found := false
	for _, tm := range tmsFetched {
		if tm.ID == tm1.ID {
			tm1Found = true
		}
		if tm.ID == tm2.ID {
			tm2Found = true
		}
	}
	if !tm1Found || !tm2Found {
		t.Fatalf("expected both token mappings to be returned")
	}

	tmFetched, err = ts.MemDBTokenMappingByTokenID("testtokenid", true)
	if err != nil {
		t.Fatal(err)
	}

	if tmFetched == tm1 {
		t.Fatalf("different reference expected")
	}

}

func TestTokenStore_MemDBDeleteTokenMappingByTokenID(t *testing.T) {
	var err error
	_, ts, _, _ := TestCoreWithTokenStore(t)

	tm1 := &token.TokenMapping{
		ID:       "testid",
		TokenID:  "testtokenid",
		Accessor: "testaccessor",
		ParentID: "testparentid",
	}
	err = ts.UpsertTokenMapping(tm1, true)
	if err != nil {
		t.Fatal(err)
	}

	err = ts.DeleteTokenMappingByTokenID("testtokenid")
	if err != nil {
		t.Fatal(err)
	}

	tmFetched, err := ts.MemDBTokenMappingByTokenID("testtokenid", false)
	if err != nil {
		t.Fatal(err)
	}

	if tmFetched != nil {
		t.Fatalf("expected a nil token mapping")
	}
}
