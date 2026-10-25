package voice

import (
	"strings"
	"unicode"
)

// Utterances longer than this are conversation, not a request to hang up.
const maxEndUtteranceWords = 6

// Any of these anywhere in a short utterance ends the call.
var endPhrases = []string{
	"заверши разговор", "завершай разговор", "заверши звонок", "завершить разговор",
	"закончи разговор", "закончим разговор", "заканчивай разговор", "закончи звонок",
	"прекрати разговор", "останови разговор", "стоп разговор", "положи трубку",
	"повесь трубку", "конец связи", "конец разговора", "давай заканчивать",
	"на этом все", "на сегодня все", "до свидания", "до встречи", "до связи",
	"пока пока", "всего доброго", "отбой",
}

// The whole utterance, after dropping fillers, must be one of these.
var endWords = map[string]bool{"пока": true, "бывай": true, "прощай": true}

var endFillers = map[string]bool{
	"ну": true, "ладно": true, "хорошо": true, "ок": true, "окей": true, "спасибо": true,
	"давай": true, "тогда": true, "все": true, "чат": true, "гпт": true, "джипити": true,
}

func normalizeUtterance(text string) []string {
	text = strings.ReplaceAll(strings.ToLower(text), "ё", "е")
	return strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// isEndRequest reports whether a finished user utterance asks to end the call.
func isEndRequest(text string) bool {
	words := normalizeUtterance(text)
	if len(words) == 0 || len(words) > maxEndUtteranceWords {
		return false
	}
	joined := " " + strings.Join(words, " ") + " "
	for _, phrase := range endPhrases {
		if strings.Contains(joined, " "+phrase+" ") {
			return true
		}
	}
	var rest []string
	for _, word := range words {
		if !endFillers[word] {
			rest = append(rest, word)
		}
	}
	return endWords[strings.Join(rest, " ")]
}
