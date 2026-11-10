# Go backend standards

These are the house rules for this backend. They are short on purpose. The census proof of concept stays a few Go files behind one local HTTP server. These rules say how that code is written, how a run keeps its data consistent, and which tests are worth writing.

## Sources

Claims below come from these documents. When they disagree, the rule in this file says which one we follow.

| Source | What we take from it |
|---|---|
| [Effective Go](https://go.dev/doc/effective_go) | `gofmt`, names, interfaces, errors as values, `panic` stays inside a package |
| [Go Doc Comments](https://go.dev/doc/comment) | A comment immediately above a declaration, with no blank line, is the doc comment. The first sentence starts with the name being declared |
| [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) | `gofmt` and `goimports`, comment sentences, `context.Context` as the first parameter, useful test failures |
| [Go Test Comments](https://go.dev/wiki/TestComments) | No assert libraries. Compare whole values. Print got, then want. Name the function and the input |
| [TableDrivenTests](https://go.dev/wiki/TableDrivenTests) | One table when many cases share the same check. A table is a way to write a careful test once, not a reason to invent cases |
| [Google Go Style Guide](https://google.github.io/styleguide/go/guide) | Clarity, simplicity, concision, maintainability, consistency. This is Google's house style. The Go project does not require it |
| [Google Go Style Best Practices](https://google.github.io/styleguide/go/best-practices) | Package size, error structure, tests that fail inside `Test`, small interfaces owned by the caller, do not add an interface only so a test can mock it |
| [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md) | Handle each error once, wrap with `%w`, do not panic in production paths, copy slices and maps at boundaries, do not put branching logic inside a table test |
| [Executing transactions](https://go.dev/doc/database/execute-transactions) | A transaction commits every change or leaves the database unchanged |
| [Contexts and structs](https://go.dev/blog/context-and-structs) | Pass `context.Context` into the call. Do not store it on a struct |
| [Package names](https://go.dev/blog/package-names) | A package is one idea. The name is a short, lowercase word |

Two disagreements are settled here:

* Uber's test examples use `assert` and `require`. The Go wiki and Google both say assertion helpers hide what failed. We follow the Go wiki and Google.
* Google's best practices say not to list every obvious parameter in prose. This project still uses the tagged block below, because every function has to state its inputs and outputs in one place. The sentence above the tags carries the non-obvious part. The tags carry the contract.

## Principles

Google's style guide weighs code on five points: clarity, simplicity, concision, maintainability, and consistency. For this backend that means:

* The reader can see what a function changes in the census state without reconstructing the call stack.
* Use the smallest thing that does the job. A function before a type. A concrete type before an interface. The standard library before a framework.
* Names and comments earn their length. A long name is not documentation.
* A later change to parsing should not require a rewrite of reconciliation.
* New code matches the files already in `backend/`. `gofmt` is the mechanical part of that. These rules are the rest.

## Layout

The proof of concept is one `package main` split by responsibility across files: the server, the pipeline, parsing, model extraction, reconciliation, state, and the run log. Google's package-size guidance says a file should be findable and should not be thousands of lines, and that Go has no "one type, one file" rule. Effective Go says a package name is one lowercase word and is the directory name.

Stay in one package while the only caller is `main`. Split a package out when a second caller needs a boundary, or when leaving the files together would create an import cycle. Do not introduce `internal/` layers, repositories, or service interfaces ahead of that need. Google's best practices say an interface added only for a future test is a common mistake.

The pipeline function is the product: one document plus the current employees, dependents, and open issues goes in, and the next state comes out. HTTP is a thin wrapper around that function.

## Formatting and names

Run `gofmt`. Use `goimports` so import lines stay exact. Effective Go and the code-review wiki both treat formatting as solved by the tool.

* Exported names use `MixedCaps`. Unexported names use `mixedCaps`. No underscores in Go names.
* Package names are short and lowercase. Exported names do not repeat the package name.
* Getters do not start with `Get`. A field `owner` is read with `Owner` and set with `SetOwner`.
* A one-method interface is an agent noun: `Extractor`, `Store`. Give it the standard library method name when the meaning matches (`Read`, `Write`, `String`).
* Error strings are lowercase and have no trailing punctuation, because callers wrap them. Initialisms stay as `SSN`, not `Ssn`.
* Keep lines readable. Uber's 99-character line is a soft aim, not a lint failure.

## Comments

Go has two comment forms. Effective Go says `//` line comments are the usual form, and `/* */` block comments are mostly used for package comments. Both forms count as a doc comment when they sit directly above a top-level declaration with no blank line. `go doc` and gopls read that comment. The first sentence must start with the declared name and end with a period, or the generated summary is wrong.

This project adds a tagged block inside that same comment. Every function and method, exported or not, has one. Put the comment immediately above the function. Do not leave a blank line between the comment and the `func` line. Do not nest `/* */` inside it. `gofmt` keeps the block, so write it in the shape below.

```go
/*
Run applies one document to the current company state and returns the next state.

* @brief: Run -> applies one document to the current company state
* @params: ctx [context.Context] -> cancellation and deadline for this run
* @params: document [Document] -> one census file or one email reply
* @params: state [State] -> employees, dependents, and open issues before this document
* @returns: next [State] -> employees, dependents, and open issues after this document
* @returns: err [error] -> non-nil when the document was not applied and state was left unchanged
* @api: /run [POST] -> accepts one document and the company id, and returns the next state
*/
func Run(ctx context.Context, document Document, state State) (next State, err error) {
```

Rules for the tags:

* The first line inside the block is the Go doc sentence. It starts with the function name.
* `@brief` repeats the name, then what the function does in one clause. The tag is `@brief`, not a misspelling.
* One `@params` line per parameter, in signature order: `name [type] -> what this value means to the caller`.
* One `@returns` line per result, in signature order, including `err`.
* `@api` is optional and only for a function that serves an HTTP route: `/route [GET|POST|...] -> what the route does`.
* A package comment uses the same block once, in one file. Its first sentence starts with `Package name` or, for `package main`, with the program name. No `@params` on a package comment.
* Types and consts that are part of the pipeline contract get the same block, with `@params` omitted and `@returns` describing the value the type holds.

The sentence says what is easy to get wrong. The tags list the contract. Do not use the tags to restate `string` as "a string".

## Errors, context, and data consistency

Errors are values. Return them. Uber and Effective Go both say production code does not panic for a bad document, a model failure, or a disk error. `panic` is for a bug that means the process cannot continue. Check the two-result form of a type assertion (`value, ok := x.(T)`). A bare assertion panics.

Handle each error once. Uber's rule: do not log an error and also return it. Wrap with `fmt.Errorf("stage: %w", err)` when the caller needs the path. Use `errors.Is` or `errors.As` when a caller must branch. Google's best practices say to put structure on an error when a caller has to tell conditions apart, instead of matching on the text.

`context.Context` is the first parameter of any function that reads state, calls the model, or writes. Take it from the HTTP request. Do not store it in a struct. A cancelled context returns `ctx.Err()`. Set a deadline on the model call.

The consistency rule for a run comes from the database tutorial's transaction definition, applied to the three tables even while they are local files: every change from one document commits together, or the stored state is left as it was.

* Read employees, dependents, and open issues at the start. That snapshot is what the log calls the read.
* Compute the next state in memory.
* Write the three tables only after format, map, and reconcile have finished. A failure in any earlier stage writes nothing to those tables.
* The run log is different. It is appended as each stage finishes, including on failure, because the prototype requires every extract, format, map, read, and write to be traceable.
* Copy slices and maps when state crosses a function boundary. Uber's rule: the caller must not be able to change a table the server has stored by editing the slice it passed in.
* A second request for the same company waits until the first write has finished. Two runs must not interleave writes to the same three files.
* Reconciliation does not invent a missing value. A blank stays unknown. A conflicting date of birth or SSN stays an open issue with both claims. That is the consistency rule for census facts, not only for files.

When this moves to Postgres, the same rule is one `BeginTx`, then the writes, then `Commit`. `Rollback` on any error. `context` goes into `BeginTx` and every statement. The pipeline function should not know whether the tables are JSON files or SQL.

## Modularity

Each file owns one stage and returns plain data to `pipeline.go`. `pipeline.go` is the only place that orders the stages and writes the log between them. Parsing does not call the model. The model call does not write tables. Reconciliation does not read the raw file.

Define an interface in the file that calls it, and only when a second implementation exists. The extractor is the case that qualifies: the HTTP handler and the tests both need to supply a model response. Keep that interface to the one method the pipeline calls. Google's best practices and the Go proverb agree: the consumer declares the small interface, and the bigger the interface, the weaker it is. Return the concrete state struct from the pipeline. Do not return an interface.

Do not share mutable package-level tables. Pass state in. Uber says to avoid mutable globals. `init` does not open files, read the environment, or start goroutines.

## Simplicity and scale

Scale for this server means one request does a bounded amount of work and does not corrupt another request. It does not mean a worker pool, a queue, or a framework.

* One request handles one document. The handler calls the pipeline and returns.
* Do not start a goroutine and walk away. Uber forbids fire-and-forget goroutines. If a later version needs background work, that version waits for it and cancels it with the request context.
* The model sees the parsed preview the pipeline built, not an unbounded copy of a multi-thousand-row file pasted into the prompt. The reconciler still applies the structured result to the full state.
* Prefer the standard library: `net/http`, `encoding/json`, `encoding/csv`, `context`, `errors`, `log`. Add a dependency when the standard library cannot do the job. The OpenAI call is an HTTP request with a JSON body.
* Copying a small struct between stages is cheaper than a shared mutable object. Rob Pike's Go proverb applies: a little copying is better than a little dependency.

The function signature stays `f(document, state) = new_state` so a queue or a frontend can call it later without a rewrite.

## Testing

Tests prove the prototype's requirements. They do not exist to touch every function.

Write a test when it locks one of these:

* One document and a known state produce a known next state.
* A document resolves an open issue, raises one, or leaves the issue list unchanged.
* An email reply is accepted as a document.
* A failed stage leaves the stored tables unchanged and still writes a log that shows how far the run got.
* The log contains the read, the extracted JSON, the formatted values, the mapping, and the writes.
* `POST /run` and `GET /logs/{id}` return those results. Use `net/http/httptest` so the test does not open a port.
* Two overlapping runs for one company do not tear the three tables.

The example files and their READMEs are the fixture list. A test may use a README's expected outcome as the wanted state. The test must not send that README, or any answer key, into the extractor. Small company files come first.

How to write them:

* Put tests in `_test.go` files. `go test` runs `TestXxx`.
* Prefer an external test package (`package main_test`) for the pipeline and the HTTP routes, so the test uses the same surface a caller uses. White-box tests of an unexported helper are for a parser or formatter rule that is awkward to see from the outside, not as a default.
* One requirement is one test function or one named subtest. `t.Run("company B payroll then enrollment", ...)`. The name is the requirement, not `TestHelper2`.
* Use a table when several documents share the same check, as the Go wiki and Uber describe. Do not put `if` branches inside the loop to make one table cover unrelated requirements. Split those.
* Compare the whole state with `cmp.Diff` when the value is a struct, slice, or map. The Go wiki says field-by-field asserts hide the shape of the result. Print got before want: `t.Errorf("Run(%s) state mismatch (-want +got):\n%s", name, diff)`.
* Do not add an assert library. Google and the Go wiki both reject them. Fail in the `Test` function.
* Do not call the live model in `go test`. Pass a fixed extraction JSON through the extractor interface. A test that needs the network is a separate, explicit check, not part of the default suite.
* A failure message names the function, the input, what came back, and what the requirement said should come back. The Go wiki says the person debugging the failure is not you.
* Skip a test that would only assert a getter, a constructor, or a log line that no requirement mentions.

`go test ./...` must pass without a network and without a Postgres server for the file-backed proof of concept.
.