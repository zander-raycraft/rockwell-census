Concept

Build this as a command line tool first, running on local servers. Once that works, add a lightweight local web frontend and backend on top of the same system. This version stays on the local machine. There is no need to deploy it to hosted services.

Messy, unstructured documents come in, and sensible structured information comes out. The run must also keep a complete log of every step. That log has to show exactly what was extracted, what was formatted, what was analyzed and mapped onto rows in the database, what was written, and what was read. Logging all of this is a hard requirement, and it must be possible to follow each of those steps while the run is happening.

Everything below is also a hard requirement and must be met.



To recap the task here, the deliverable is:
* A program that takes in a SINGLE unstructured document plus the current state of tables (employees, dependents, open issues), and outputs the new state. A document may resolve open issues, raise new ones, or neither.
    * That is, create `f`, where `f(document, state) = new_state`, with `state = (employees_table, dependents_table, open_issues_table)`.
    * We want to be able to see the state evolve each step of the way, and verify against that.
    * Prioritize using small examples for testability and evaluability.
* Notes:
    * The idea is that we get one of these documents from our clients, feed it into the system, and see/surface any issues that come up.
* Note - an email containing a quick answer like "Yes, Bob Moses and Robert Moses are the same person, and his SSN is actually xxx-xx-xxxx" is a potential input.
* Lastly, you could build a 'data provenance' system on top of this so that we can track where each fact came from (e.g. which document maps to which fact), and that is useful in real life, but that is outside of the scope of this exercise.
* I'll make some private examples we can use to evaluate your system on. How easily we can change your system to adapt these new examples will be an important part of deciding how good it is.