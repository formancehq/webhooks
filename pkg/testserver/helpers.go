package testserver

import (
	//nolint:staticcheck
	. "github.com/formancehq/go-libs/v2/testing/utils"
	//nolint:staticcheck
	. "github.com/onsi/ginkgo/v2"
)

func NewTestServer(configurationProvider func() Configuration) *Deferred[*Server] {
	d := NewDeferred[*Server]()
	BeforeEach(func() {
		d.Reset()
		d.SetValue(New(GinkgoT(), configurationProvider()))
	})
	return d
}
