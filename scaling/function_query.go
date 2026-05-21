// License: OpenFaaS Community Edition (CE) EULA
// Copyright (c) 2017,2019-2024 OpenFaaS Author(s)

// Copyright (c) OpenFaaS Author(s). All rights reserved.

package scaling

type DirectFunctionQuery struct {
	serviceQuery     ServiceQuery
	emptyAnnotations map[string]string
}

func NewDirectFunctionQuery(serviceQuery ServiceQuery) FunctionQuery {
	return &DirectFunctionQuery{
		serviceQuery:     serviceQuery,
		emptyAnnotations: map[string]string{},
	}
}

func (c *DirectFunctionQuery) GetAnnotations(name string, namespace string) (annotations map[string]string, err error) {
	res, err := c.Get(name, namespace)
	if err != nil {
		return c.emptyAnnotations, err
	}

	if res.Annotations == nil {
		return c.emptyAnnotations, nil
	}
	return *res.Annotations, nil
}

func (c *DirectFunctionQuery) Get(fn string, ns string) (ServiceQueryResponse, error) {
	return c.serviceQuery.GetReplicas(fn, ns)
}

type FunctionQuery interface {
	Get(name string, namespace string) (ServiceQueryResponse, error)
	GetAnnotations(name string, namespace string) (annotations map[string]string, err error)
}
