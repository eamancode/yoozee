package assets

import "testing"

func TestVideoChainFrameLineageIsProtectedReference(t *testing.T) {
	refs, err := CollectDocumentResourceReferences(`{"steps":[{"frameResourceId":"frame","videoResourceId":"output","sourceVideoResourceId":"source"}],"error":"frame-not-reference"}`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, ref := range refs {
		got[ref.ResourceID] = true
	}
	if len(got) != 3 || !got["frame"] || !got["source"] || !got["output"] {
		t.Fatal("chain lineage not protected", refs)
	}
}

func TestCollectDocumentResourceReferencesIncludesNestedVideoPreview(t *testing.T) {
	raw := `{"nodes":[{"id":"node-video","metadata":{"videoPreview":{"storageKey":"resource:poster-1","source":{"id":"nested-not-node","url":"/api/resources/poster-2/file"}}}}]}`
	refs, err := CollectDocumentResourceReferences(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DocumentResourceReference{}
	for _, ref := range refs {
		got[ref.ResourceID] = ref
	}
	if ref := got["poster-1"]; ref.Path != "nodes[node-video].metadata.videoPreview.storageKey" || ref.NodeID != "node-video" || ref.ReferenceType != "storageKey" {
		t.Fatalf("nested storageKey reference = %#v", ref)
	}
	if ref := got["poster-2"]; ref.Path != "nodes[node-video].metadata.videoPreview.source.url" || ref.NodeID != "node-video" {
		t.Fatalf("nested URL reference = %#v", ref)
	}
}

func TestCollectDocumentResourceReferencesUsesExplicitFieldsOnly(t *testing.T) {
	raw := `{"nodes":[{"id":"node-1","metadata":{"label":"resource:not-a-reference","videoPreview":{"resourceId":"poster-3"}}}],"log":"/api/resources/log-1/file"}`
	refs, err := CollectDocumentResourceReferences(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ResourceID != "poster-3" || refs[0].NodeID != "node-1" || refs[0].ReferenceType != "resourceId" {
		t.Fatalf("references = %#v", refs)
	}
}

func TestCollectDocumentResourceReferencesReportsInvalidJSON(t *testing.T) {
	if _, err := CollectDocumentResourceReferences(`{"nodes":`); err == nil {
		t.Fatal("expected malformed document to be rejected")
	}
}
