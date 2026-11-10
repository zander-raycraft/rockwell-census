# Rockwell web

React and strict TypeScript on Vite. Open a company, select or drop a batch of files, then choose **Process documents**. Each file is sent to the existing pipeline in order. A failed file stops the batch and keeps the remaining selection.

```sh
cd web
npm install
npm run dev
```

Open http://localhost:5173. The Vite proxy reads `BACKEND_URL` from `web/.env` (defaults to http://localhost:8080). Restart Vite after changing it. The backend must be running with the `/runs` route included.

- **Run history:** saved document runs from backend logs; survives page reloads.
- **Process:** one expandable job with readable stage results. Click a timing button to isolate that stage and its recorded calls; choose All steps to return to the overview. Exact inputs, outputs, model requests and SQL evidence stay available underneath each step. Active traces refresh about every 1.5 seconds; the run list refreshes every 2 seconds. No progress is simulated.
- **Issues:** current saved open/resolved issues, retained claims, and committed changes grouped by processing document. History covers available traces, not deleted logs. A processing document can release evidence retained from an earlier document.
- **Saved census:** readable employee, dependent and coverage tables. Export PDF opens a print-ready view; choose Save as PDF in the browser dialog.
- **Replies:** type a natural-language correction on the Issues tab and submit it as an email-style document. It goes through the existing extraction and reconciliation pipeline and appears as its own job. Failed replies stay available to edit or retry.

Files stay in browser memory until explicitly submitted. Census data and traces are not copied to browser storage. Switching company or unmounting cancels pending browser requests; an already committed server operation may still appear in history.

`npm run build` type-checks and builds the app. `npm test` runs fixed-response UI checks without a backend or model. `npm run format` runs Prettier. Production hosting must forward `/api/*` to the Go server with the `/api` prefix removed.
