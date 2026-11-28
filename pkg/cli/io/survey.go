package io

import (
	"errors"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

// SurveyStdio returns a survey option that reads answers from streams.In and
// writes prompts to streams.ErrOut, so that a command's output stream carries
// only its output. Survey requires both streams to be files, such as a
// terminal. If either is not, prompting fails with an error.
func SurveyStdio(streams genericiooptions.IOStreams) survey.AskOpt {
	return func(opts *survey.AskOptions) error {
		in, ok := streams.In.(terminal.FileReader)
		if !ok {
			return errors.New("cannot prompt: the input stream is not a file")
		}
		out, ok := streams.ErrOut.(terminal.FileWriter)
		if !ok {
			return errors.New(
				"cannot prompt: the error output stream is not a file",
			)
		}
		return survey.WithStdio(in, out, out)(opts)
	}
}
