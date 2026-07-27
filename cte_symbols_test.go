package main

import (
	"context"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestDocumentSymbolIncludesCTEsAndOutputColumns(t *testing.T) {
	const text = `WITH LocalRows AS (
  SELECT SingerId AS Id, Name FROM Singers
)
SELECT Id FROM LocalRows`
	h := newParsedTestHandler(t, "/query.sql", text)

	got, err := h.DocumentSymbol(context.Background(), &protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("DocumentSymbol() = %#v, want one CTE", got)
	}
	cte := got[0].(protocol.DocumentSymbol)
	if cte.Name != "LocalRows" || cte.Detail != "CTE" ||
		cte.Kind != protocol.Struct || len(cte.Children) != 2 {
		t.Fatalf("CTE symbol = %#v, want LocalRows with two columns", cte)
	}
	if cte.Children[0].Name != "Id" || cte.Children[0].Kind != protocol.Field ||
		cte.Children[1].Name != "Name" || cte.Children[1].Kind != protocol.Field {
		t.Fatalf("CTE children = %#v, want Id and Name fields", cte.Children)
	}
	if !rangeContains(cte.Range, cte.SelectionRange) ||
		!rangeContains(cte.Range, cte.Children[0].SelectionRange) {
		t.Fatalf("CTE ranges = %#v, children %#v", cte, cte.Children)
	}
}

func TestDocumentSymbolNestsCTEsAndPreservesSourceOrder(t *testing.T) {
	const text = `CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);
WITH OuterRows AS (
  WITH InnerRows AS (SELECT SingerId AS Id FROM Singers)
  SELECT Id FROM InnerRows
)
SELECT Id FROM OuterRows`
	h := newParsedTestHandler(t, "/query.sql", text)

	got, err := h.DocumentSymbol(context.Background(), &protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("DocumentSymbol() = %#v, want table and outer CTE", got)
	}
	table := got[0].(protocol.DocumentSymbol)
	outer := got[1].(protocol.DocumentSymbol)
	if table.Name != "Singers" || outer.Name != "OuterRows" {
		t.Fatalf("DocumentSymbol() order = %#v, want Singers then OuterRows", got)
	}
	var nested *protocol.DocumentSymbol
	for i := range outer.Children {
		if outer.Children[i].Name == "InnerRows" {
			nested = &outer.Children[i]
			break
		}
	}
	if nested == nil || nested.Detail != "CTE" {
		t.Fatalf("OuterRows children = %#v, want nested InnerRows CTE", outer.Children)
	}
}
