// License: OpenFaaS Community Edition (CE) EULA
// Copyright (c) 2017,2019-2024 OpenFaaS Author(s)

package scaling

import "testing"

func TestDirectFunctionQueryGetsLiveAnnotations(t *testing.T) {
	firstAnnotations := map[string]string{"version": "one"}
	secondAnnotations := map[string]string{"version": "two"}
	serviceQuery := &fakeServiceQuery{
		responses: []ServiceQueryResponse{
			{Annotations: &firstAnnotations},
			{Annotations: &secondAnnotations},
		},
	}
	query := NewDirectFunctionQuery(serviceQuery)

	first, err := query.GetAnnotations("echo", "openfaas-fn")
	if err != nil {
		t.Fatalf("unexpected first annotation error: %s", err)
	}
	second, err := query.GetAnnotations("echo", "openfaas-fn")
	if err != nil {
		t.Fatalf("unexpected second annotation error: %s", err)
	}

	if first["version"] != "one" {
		t.Fatalf("expected first live annotations, got %v", first)
	}
	if second["version"] != "two" {
		t.Fatalf("expected second live annotations, got %v", second)
	}
	if serviceQuery.getCalls != 2 {
		t.Fatalf("expected two live GetReplicas calls, got %d", serviceQuery.getCalls)
	}
}
