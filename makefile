# start project configuration
name := pail
buildDir := build
srcFiles := $(shell find . -name "*.go" -not -path "./$(buildDir)/*" -not -name "*_test.go" -not -path "*\#*")
testFiles := $(shell find . -name "*.go" -not -path "./$(buildDir)/*" -not -path "*\#*")
orgPath := github.com/evergreen-ci
projectPath := $(orgPath)/$(name)
_testPackages := ./ 
# end project configuration


testArgs := -v
ifneq (,$(RUN_TEST))
testArgs += -run='$(RUN_TEST)'
endif
ifneq (,$(RUN_COUNT))
testArgs += -count='$(RUN_COUNT)'
endif
ifneq (,$(SKIP_LONG))
testArgs += -short
endif
ifneq (,$(DISABLE_COVERAGE))
testArgs += -cover
endif
ifneq (,$(RACE_DETECTOR))
testArgs += -race
endif

# start linting configuration
#   package, testing, and linter dependencies specified
#   separately. This is a temporary solution: eventually we should
#   vendorize all of these dependencies.
lintDeps := github.com/alecthomas/gometalinter
#   include test files and give linters 40s to run to avoid timeouts
lintArgs := --tests --deadline=14m --vendor
#   gotype produces false positives because it reads .a files which
#   are rarely up to date.
lintArgs += --disable="gotype" --disable="gosec" --disable="gocyclo" --disable="golint"
lintArgs += --disable="megacheck" --enable="unused" --enable="gosimple"
lintArgs += --skip="build"
#   enable and configure additional linters
lintArgs += --line-length=100 --dupl-threshold=150 --cyclo-over=15
#   the gotype linter has an imperfect compilation simulator and
#   produces the following false postive errors:
lintArgs += --exclude="error: could not import github.com/mongodb/greenbay"
#   some test cases are structurally similar, and lead to dupl linter
#   warnings, but are important to maintain separately, and would be
#   difficult to test without a much more complex reflection/code
#   generation approach, so we ignore dupl errors in tests.
lintArgs += --exclude="warning: duplicate of .*_test.go"
#   go lint warns on an error in docstring format, erroneously because
#   it doesn't consider the entire package.
lintArgs += --exclude="warning: package comment should be of the form \"Package .* ...\""
#   known issues that the linter picks up that are not relevant in our cases
lintArgs += --exclude="file is not goimported" # top-level mains aren't imported
lintArgs += --exclude="error return value not checked .defer.*"
lintArgs += --exclude="\w+Key is unused.*"
lintArgs += --exclude="unused global variable \w+Key"
lintArgs += --exclude=".*unused variable or constant \w+Key"
# end linting configuration


# start dependency installation tools
#   implementation details for being able to lazily install dependencies
gopath := $(shell go env GOPATH)
lintDeps := $(addprefix $(gopath)/src/,$(lintDeps))
$(buildDir)/run-linter:cmd/run-linter/run-linter.go $(buildDir)/.lintSetup
	 go build -o $@ $<
$(buildDir)/.lintSetup:$(lintDeps)
	@-$(gopath)/bin/gometalinter --install >/dev/null && touch $@
# end dependency installation tools


# implementation details for building the binary and creating a
# convienent link in the working directory
$(name):$(buildDir)/$(name)
	@[ -e $@ ] || ln -s $<
$(buildDir)/$(name):$(srcFiles)
	go build -ldflags "-X github.com/evergreen-ci/pail.BuildRevision=`git rev-parse HEAD`" -o $@ cmd/$(name)/$(name).go
# end dependency installation tools


# distribution targets and implementation
dist:$(buildDir)/dist.tar.gz
$(buildDir)/dist.tar.gz:$(buildDir)/$(name)
	tar -C $(buildDir) -czvf $@ $(name)
# end main build


# userfacing targets for basic build and development operations
proto:
	@mkdir -p rpc/internal
	protoc --go_out=plugins=grpc:rpc/internal *.proto
lint:$(foreach target,$(_testPackages),$(buildDir)/output.$(target).lint)
test:$(buildDir)/output.test
build:$(buildDir)/$(name)
coverage:$(coverageOutput)
coverage-html:$(coverageHtmlOutput)
list-tests:
	@echo -e "test targets:" $(foreach target,$(_testPackages),\\n\\ttest-$(target))
phony += lint lint-deps build build-race race test coverage coverage-html list-race list-tests
.PRECIOUS:$(coverageOutput) $(coverageHtmlOutput)
.PRECIOUS:$(foreach target,$(_testPackages),$(buildDir)/output.$(target).test)
.PRECIOUS:$(foreach target,$(_testPackages),$(buildDir)/output.$(target).lint)
.PRECIOUS:$(buildDir)/output.lint
# end front-ends

compile:
	go build $(_testPackages)
test:$(buildDir)/test.out
$(buildDir)/test.out:.FORCE
	@mkdir -p $(buildDir)
	go test $(testArgs) $(_testPackages) | tee $@
	@grep -s -q -e "^PASS" $@
coverage:$(buildDir)/cover.out
	@go tool cover -func=$< | sed -E 's%github.com/.*/ftdc/%%' | column -t
coverage-html:$(buildDir)/cover.html

benchmark:
	go test -v -benchmem -bench=. -run="Benchmark.*" -timeout=20m

$(buildDir):$(srcFiles) compile
	@mkdir -p $@
$(buildDir)/cover.out:$(buildDir) $(testFiles) .FORCE
	go test $(testArgs) -covermode=count -coverprofile $@ -cover ./
$(buildDir)/cover.html:$(buildDir)/cover.out
	go tool cover -html=$< -o $@
.FORCE:

# test execution and output handlers
$(buildDir)/:
	mkdir -p $@
$(buildDir)/output.%.test:$(buildDir)/ .FORCE
	go test $(testArgs) ./$(if $(subst $(name),,$*),$*,) | tee $@
	@! grep -s -q -e "^FAIL" $@ && ! grep -s -q "^WARNING: DATA RACE" $@
$(buildDir)/output.test:$(buildDir)/ .FORCE
	go test $(testArgs) ./... | tee $@
	@! grep -s -q -e "^FAIL" $@ && ! grep -s -q "^WARNING: DATA RACE" $@
$(buildDir)/output.%.coverage:$(buildDir)/ .FORCE
	go test $(testArgs) ./$(if $(subst $(name),,$*),$*,) -covermode=count -coverprofile $@ | tee $(buildDir)/output.$*.test
	@-[ -f $@ ] && go tool cover -func=$@ | sed 's%$(projectPath)/%%' | column -t
$(buildDir)/output.%.coverage.html:$(buildDir)/output.%.coverage
	go tool cover -html=$< -o $@
#  targets to generate gotest output from the linter.
$(buildDir)/output.%.lint:$(buildDir)/run-linter $(buildDir)/ .FORCE
	@./$< --output=$@ --lintArgs='$(lintArgs)' --packages='$*'
$(buildDir)/output.lint:$(buildDir)/run-linter $(buildDir)/ .FORCE
	@./$< --output="$@" --lintArgs='$(lintArgs)' --packages="$(packages)"
#  targets to process and generate coverage reports
# end test and coverage artifacts


vendor-clean:
	find vendor/ -name "*.gif" -o -name "*.gz" -o -name "*.png" -o -name "*.ico" -o -name "*testdata*" | xargs rm -rf
	rm -rf vendor/github.com/mongodb/grip/vendor/github.com/stretchr/testify
