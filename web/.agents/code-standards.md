# React TypeScript frontend standards

These are the house rules for this frontend. They are short on purpose. The census screen is a Vite app in `web/`: one place to choose documents, send a document to the local server, and show the employees, dependents, enrollments, and open issues that came back. These rules say how that code is written, what the screen is allowed to invent, and which tests are worth writing.

## Sources

Claims below come from these documents. When they disagree, the rule in this file says which one we follow.

| Source | What we take from it |
|---|---|
| [React rules](https://react.dev/reference/rules) | Function components, pure render, React calls components, Hooks stay at the top level |
| [Rules of Hooks](https://react.dev/reference/rules/rules-of-hooks) | A Hook runs from a component or another Hook, before any early return |
| [You Might Not Need an Effect](https://react.dev/learn/you-might-not-need-an-effect) | Derive values during render. Handle a click in the handler. An Effect synchronizes with something outside React |
| [Accessibility](https://react.dev/learn/accessibility) | Semantic HTML first. An accessible name on every control |
| [TypeScript Handbook](https://www.typescriptlang.org/docs/handbook/2/everyday-types.html) | Everyday types, narrowing, `strict` null checks |
| [`strict`](https://www.typescriptlang.org/tsconfig/strict.html) | One compiler flag for the strict family, including `strictNullChecks` and `noImplicitAny` |
| [Do's and Don'ts](https://www.typescriptlang.org/docs/handbook/declaration-files/do-s-and-don-ts.html) | `string` rather than `String`. `unknown` when the value is unchecked. `void` for a callback whose result is ignored |
| [Google TypeScript Style Guide](https://google.github.io/styleguide/tsguide.html) | Named exports, interfaces for object shapes, `T[]` for simple arrays, JSDoc on exports, no `const enum`, no wrapper objects |
| [Airbnb React/JSX](https://github.com/airbnb/javascript/blob/master/react/README.md) | JSX, and a function declaration for a component. The class and PropTypes sections are from the JavaScript guide and do not apply here |
| [Testing Library](https://testing-library.com/docs/) | A test resembles the way the screen is used. Query by role and accessible name |
| [Which query](https://testing-library.com/docs/queries/about/#priority) | Role, then label, then text. `data-testid` is the last resort |
| [Prettier](https://prettier.io/docs/) | Formatting is a tool, the same job `gofmt` does for the backend |
| [Vite](https://vite.dev/guide/) | The dev server, the build, and the module runner for this app |
| [Vitest](https://vitest.dev/guide/) | The test runner that fits a Vite project |

Five disagreements are settled here:

* Airbnb's older pages prefer a class once a component has state, and they use PropTypes. Current React components are functions, and this project uses TypeScript for the prop contract. We follow the React docs and TypeScript.
* Airbnb's one-component-per-file rule fits a component library. This screen stays in a few files until a second screen needs its own file. Google's export rule still holds: export a symbol when another module uses it.
* Google's file names are `snake_case`, and Google forbids a default export. React code in this repo names a component file after the component, in `UpperCamelCase`, and the entry file stays `main`. We follow Google on exports: every export is named. We do not rename files to `snake_case`.
* Google allows a plain `enum` and forbids `const enum`. A closed census value that arrives as JSON, such as a document intent or an issue status, is a string union. The union matches the payload and needs no runtime object. We do not use `const enum`. We do not add an `enum` for a set that is already a union.
* Google says a parameter comment can be omitted when the name and the signature already say the meaning, and it says a JSDoc tag must not repeat the TypeScript type. This project still uses the tagged block below on every function, with the type in brackets, because the backend states each contract in one place. The sentence above the tags carries the non-obvious part. The tags carry the contract. The signature still wins if a tag drifts.

## Principles

The screen has one job: show the census the server committed, and show a failure without replacing that census. For this frontend that means:

* The reader can see what a user action changes without reconstructing a chain of Effects.
* Use the smallest thing that does the job. A function before a component. Local state before a store. The platform before a library.
* Names and comments earn their length. A long name is not documentation.
* A later change to the upload control should not require a rewrite of the census view.
* New code matches the files already in `web/`. Prettier is the mechanical part of that. These rules are the rest.

## Layout

The app is React 19 on Vite. TypeScript source lives in `web/src`. The entry file mounts one root with `createRoot` and `StrictMode`. A component file holds the components that are only used together. A module without JSX uses `.ts`. A module with JSX uses `.tsx`.

Stay in the entry and its stylesheet while the only screen is the upload. Split a file out when a second screen needs a boundary, or when leaving the code together would mix the request with the markup. Do not add a design-system directory, a global store, or a context for a value only one component reads.

The request function is the product boundary: one document and a company name go to the local server, and the next census state comes back. The component calls that function and renders the result. It does not parse a census file and it does not reconcile people.

## Formatting and names

Run Prettier. Turn on `"strict": true` in `tsconfig`. Use `import type` for a symbol that is only a type, as the Google guide describes, so a type import is not a runtime import.

* Component functions and types use `UpperCamelCase`. Values, Hooks, and event handlers use `lowerCamelCase`. A module-level constant that must not be reassigned uses `CONSTANT_CASE`.
* A component is a function declaration, which is the form Airbnb and the React docs show. The name starts with a capital letter so JSX can render it.
* Export the component by that name. Google's rule: a default export has no canonical name, so two files can import it under two names.
* Hooks start with `use` and follow the Rules of Hooks. A normal helper does not start with `use`.
* Do not prefix an interface with `I`. Name it for the value it holds: `CensusState`, `OpenIssue`.
* An object shape is an `interface`. A union, a tuple, or a primitive alias is a `type`. Google chooses `interface` for objects so the two forms do not vary.
* A simple array is `Employee[]`. A union element uses the long form, `Array<Employee | Dependent>`, because the brackets are easier to misread.
* Initialisms stay as `SSN` inside a name, written `ssn` when the whole name is camel case: `ssnLastFour` is wrong for this data, and `employeeSsn` hides the initialism. Use `employeeSSN` only if the name would otherwise be unclear. Prefer `ssn`.
* Boolean props and state read as predicates: `isDragging`, `hasOpenIssues`. The type already says `boolean`. The prefix says which question the value answers.
* Do not use a leading underscore on a private value. An underscore is only the unused binding Google allows: `(_file, index) => index`.
* A census field that can be missing is `string | null` at the boundary with the server, because the backend stores unknown as nil and JSON sends that as `null`. Narrow it at that boundary. A local "we have not loaded yet" value is `undefined`. Google's guide says the absent value follows the API that produced it.

## Comments

TypeScript has two comment forms. Google says `/** */` is the documentation comment an editor shows, and `//` is an implementation note. Put the documentation comment immediately above the function. Do not leave a blank line between the comment and the function. Do not nest `/* */` inside it.

This project adds the same tagged block the backend uses. Every function and component, exported or not, has one. The first sentence is the summary. It starts with the function name and uses a third-person verb, which is the form Google specifies for a method description.

```tsx
/**
 * SubmitDocument sends one file to the local census server and returns the committed state.
 *
 * @brief: SubmitDocument -> sends one document and returns the next census state
 * @params: companyName [string] -> company whose census is updated
 * @params: document [File] -> one census file or one email body
 * @params: signal [AbortSignal] -> cancellation for this request
 * @returns: state [CensusState] -> employees, dependents, enrollments, and open issues after the run
 * @api: /run [POST] -> accepts one document and a company name, and returns the next state
 */
export function SubmitDocument(companyName: string, document: File, signal: AbortSignal): Promise<CensusState> {
```

Rules for the tags:

* The first line is the summary the editor shows. It starts with the function name.
* `@brief` repeats the name, then what the function does in one clause. The tag is `@brief`, not a misspelling.
* One `@params` line per parameter, in signature order: `name [type] -> what this value means to the caller`. The type in brackets matches the signature. Google's tool tag is `@param`, and Google would omit the bracketed type. We keep `@params` and the type so this file and the Go standards describe a contract the same way.
* One `@returns` line per result. Google spells the tool tag `@return`. We spell the house tag `@returns`.
* `@api` is optional and only for a function that calls an HTTP route: `/route [GET|POST|...] -> what the route does`.
* A file that is the entry point gets one block above the mount, with no `@params`. Its first sentence starts with the app name.
* Props interfaces that cross a component boundary get the same block, with `@params` omitted and `@returns` describing the value the component shows.

The sentence says what is easy to get wrong. The tags list the contract. Do not use the tags to restate `string` as "a string".

## State, requests, and what the screen may show

Render is pure. The React rules say a component returns the same output for the same props, state, and context, and it does not change those values during render. A click, a drop, and a submit run in the event handler. An Effect is for synchronizing with a system outside React: a file dialog already closed, an `AbortController` on unmount, or the current company loaded because the screen opened. Derive a filtered list during render. Reset a subtree by giving it a new `key`.

The consistency rule for the screen matches the backend: the view shows the state a run committed, or it shows the previous state plus the error. It does not invent a census fact.

* A blank field renders as unknown. The screen does not turn a blank into waived, declined, or an empty string that looks like a known value.
* A conflicting date of birth or SSN stays visible with both claims. The screen does not pick one and hide the issue.
* A failed request leaves the census already on screen unchanged and shows the error.
* One response updates one company. A slow response for company A must not paint company B. Pass an `AbortSignal`, and ignore a response whose company is no longer the one on screen.
* Selected files stay in memory until the user removes them, clears them, or reloads. Sending them waits for an explicit submit.
* Parse JSON as `unknown` and narrow it to `CensusState` before rendering. A cast with `as CensusState` skips that check. Google and the TypeScript handbook both treat `any` as turning the checker off. Use `unknown`, then a guard.
* Do not mutate a state array in place. Copy it, then pass the copy to the setter. The same rule applies to props.
* The browser holds no API key. The local server reads the model credential. The frontend sends the document and reads the state.
* A list of people uses a stable id from the server as the React key. The index is a key only for a static list that is never reordered, which is the case the React list docs allow and this census is not.

Controls use the HTML element that already has the behavior. A button is a `button`. A file input has a label. A status that changes away from the control uses a live region, `role="status"`, which is the pattern already on the upload screen. Add an ARIA attribute when the native element cannot express the widget. Do not put `role="button"` on a `div` that should have been a button.

## Modularity

Each module owns one kind of work and returns plain data. The request module talks HTTP. A component decides what to show. A formatter turns a nullable census field into the text "unknown" or the stored value. The component does not assemble `FormData` inline in ten handlers, and the request module does not choose markup.

Keep state in the component that handles the event. Lift it when a second component renders the same value. That is the React description of sharing state, and it is the same boundary rule as the backend: pass data in, and do not keep a mutable module-level census.

A custom Hook earns its place when two components use the same stateful behavior. A Hook that exists for one component can be the function body of that component. Do not pass a Hook through props. The React rules say a Hook is called in the component, and it is not a value to hand around.

## Simplicity

Scale for this screen means one action does a bounded amount of work and a second action cannot paint over it. It does not mean a client cache, a state library, or a router full of empty pages.

* One submit sends one document. The handler calls the request function and renders the returned state.
* Do not start a request and forget it. Hold the `AbortController` and abort it when the company changes or the component unmounts.
* The browser does not receive the whole run log as part of the button click. The screen shows the committed state and the error. A log view can come later as its own request.
* Prefer the platform: `fetch`, `FormData`, `AbortController`, `File`, and CSS in the existing stylesheet. Add a dependency when the platform cannot do the job. React and Vite are the framework. A component kit, a CSS-in-JS runtime, and a global store are not required for this screen.
* Copying a small state object on an update is cheaper than a shared mutable model. The backend rule applies here too: a little copying is better than a little dependency.

The request function stays "one document in, the next state out" so the screen can call the same local server the curl checks call.

## Testing

Tests prove what a person can do on the screen and what the screen must refuse to invent. They do not exist to touch every function.

Write a test when it locks one of these:

* The user can add files, drop files, remove one file, and clear the selection.
* The same file is not listed twice.
* A submit shows the employees, dependents, enrollments, and open issues from the response.
* A failed submit leaves the census on screen unchanged and shows the error.
* An email body and a table file are both accepted as documents.
* A null or missing field renders as unknown.
* A conflicting date of birth or SSN shows both claims.
* Switching company, or leaving the screen, drops the in-flight response so it cannot paint the next view.

How to write them:

* Put tests beside the module they exercise, in `*.test.ts` or `*.test.tsx`. Vitest runs them.
* Use React Testing Library for anything the user sees. `render` the component, then `getByRole` with the accessible name. Testing Library's order is role, then label, then text. Use `data-testid` when the element has no accessible name and the markup cannot gain one.
* One requirement is one test. The name is the requirement: `it("shows both SSN claims when the server returns a conflict", ...)`.
* Use a table when several documents share the same check. Keep each row a data row. Split a case that needs its own setup.
* Compare the census text the user can read. Testing Library's principle is that a test which reaches into state or a child component breaks when the markup changes and the behavior did not. The visible result is the whole value.
* Vitest's `expect` is the assertion tool. JavaScript does not have the Go test printer, so this is the exception to the backend's ban on assertion helpers. Do not add a second matcher package. Do not use a snapshot as the proof of a requirement. A snapshot changes when the markup moves, which is the failure mode Testing Library tells you to avoid.
* Do not call the live model or a real backend in the default suite. Give `fetch` a fixed census JSON. A test that needs the network is a separate, explicit check.
* A failure should name the action, what came back, and what the requirement said should come back. The query failure already prints the DOM. Leave that output intact.
* Skip a test that would only assert a style, a class name, or a piece of state no requirement mentions.

`npm test` must pass without a network and without the census server.
