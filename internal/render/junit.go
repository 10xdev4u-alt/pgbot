package render

import (
	"encoding/xml"
	"io"
	"strings"

	"github.com/pgrundev/pgbot/internal/model"
)

// JUnit writes findings as JUnit XML for Jenkins/GitLab test panes: each finding
// is a testcase (named "<id> <object>", classed by dimension). A finding at or
// above the --fail-on threshold is a <failure>; a suppressed one is <skipped>
// (so it's visible, not silently gone); anything below the threshold is a passing
// case. failOn matches the exit-code contract, so the test pane and the exit code
// agree.
func JUnit(w io.Writer, c *model.Context, failOn string) error {
	threshold := junitRank(failOn)
	suite := junitSuite{Name: "pgbot"}
	for i := range c.Findings {
		f := &c.Findings[i]
		name := f.ID
		if f.Object != "" {
			name += " " + f.Object
		}
		tc := junitCase{Name: name, Classname: f.Impact.Dimension}
		switch {
		case f.Suppressed:
			tc.Skipped = &junitSkipped{Message: "suppressed by config: " + f.SuppressionReason}
		case failOn != "none" && junitRank(f.Severity) >= threshold:
			text := strings.TrimSpace(f.Detail + "\n" + f.Remediation)
			if s := safetyText(f); s != "" {
				text = strings.TrimSpace(text + "\n" + s)
			}
			tc.Failure = &junitFailure{
				Message: f.Title,
				Type:    f.Severity,
				Text:    text,
			}
			suite.Failures++
		}
		suite.Cases = append(suite.Cases, tc)
		suite.Tests++
	}
	doc := junitSuites{Tests: suite.Tests, Failures: suite.Failures, Suites: []junitSuite{suite}}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// junitRank orders severities for the --fail-on threshold (mirrors the exit-code
// ranking; kept local so render doesn't depend on package main).
func junitRank(sev string) int {
	switch sev {
	case model.SeverityCritical:
		return 3
	case model.SeverityWarn:
		return 2
	case model.SeverityInfo:
		return 1
	}
	return 0
}

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	XMLName  xml.Name    `xml:"testsuite"`
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}
