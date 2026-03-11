GOA_DESIGN_PKG := github.com/Sienci-Labs/gsender/machine-core/design

.PHONY: goa-gen goa-check test

goa-gen:
	goa gen $(GOA_DESIGN_PKG)

goa-check:
	goa gen $(GOA_DESIGN_PKG)
	git diff --exit-code -- gen goa489139379

test:
	go test ./...
