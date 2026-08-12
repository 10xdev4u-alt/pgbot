package model

// SchemaVersion is the semver of the Context JSON contract. Agents and scripts
// pin against it, so bump the MAJOR on any breaking field change (removed or
// renamed field, changed type or units), the MINOR on additive fields.
const SchemaVersion = "1.0.0"
