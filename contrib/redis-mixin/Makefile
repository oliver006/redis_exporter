JSONNET_FMT := jsonnetfmt -n 2 --max-blank-lines 2 --string-style s --comment-style s

# no lint for now, fails with a lot of errors and needs cleaning up first
all: deps fmt build clean

deps:
	go install github.com/monitoring-mixins/mixtool/cmd/mixtool@master
	go install github.com/google/go-jsonnet/cmd/jsonnetfmt@latest


fmt:
	find . -name 'vendor' -prune -o -name '*.libsonnet' -print -o -name '*.jsonnet' -print | \
		xargs -n 1 -- $(JSONNET_FMT) -i

lint:
	find . -name 'vendor' -prune -o -name '*.libsonnet' -print -o -name '*.jsonnet' -print | \
		while read f; do \
			$(JSONNET_FMT) "$$f" | diff -u "$$f" -; \
		done

	mixtool lint mixin.libsonnet

build:
	mixtool generate all mixin.libsonnet

clean:
	rm -rf dashboards_out alerts.yaml rules.yaml
