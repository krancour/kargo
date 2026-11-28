package io

import (
	"bytes"
	"os"
	"testing"

	"github.com/AlecAivazis/survey/v2"
	"github.com/stretchr/testify/require"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

func TestSurveyStdio(t *testing.T) {
	in, out := newPipe(t)
	testCases := []struct {
		name    string
		streams genericiooptions.IOStreams
		assert  func(*testing.T, survey.AskOptions, error)
	}{
		{
			name: "input stream is not a file",
			streams: genericiooptions.IOStreams{
				In:     &bytes.Buffer{},
				ErrOut: out,
			},
			assert: func(t *testing.T, _ survey.AskOptions, err error) {
				require.ErrorContains(t, err, "the input stream is not a file")
			},
		},
		{
			name: "error output stream is not a file",
			streams: genericiooptions.IOStreams{
				In:     in,
				ErrOut: &bytes.Buffer{},
			},
			assert: func(t *testing.T, _ survey.AskOptions, err error) {
				require.ErrorContains(
					t,
					err,
					"the error output stream is not a file",
				)
			},
		},
		{
			name: "both streams are files",
			streams: genericiooptions.IOStreams{
				In:     in,
				Out:    &bytes.Buffer{},
				ErrOut: out,
			},
			assert: func(t *testing.T, opts survey.AskOptions, err error) {
				require.NoError(t, err)
				require.Same(t, in, opts.Stdio.In)
				require.Same(t, out, opts.Stdio.Out)
				require.Same(t, out, opts.Stdio.Err)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			opts := survey.AskOptions{}
			err := SurveyStdio(testCase.streams)(&opts)
			testCase.assert(t, opts, err)
		})
	}
}

// newPipe returns the two ends of an OS pipe, which, unlike in-memory streams,
// are files.
func newPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	return r, w
}
