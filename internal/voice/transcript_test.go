package voice

import "testing"

func TestIsEndRequest(t *testing.T) {
	for _, text := range []string{
		"Заверши разговор", "заверши звонок", "Ну всё, пока!", "Спасибо, пока", "пока-пока",
		"До свидания", "Давай заканчивать", "На этом всё", "Отбой", "Положи трубку", "Конец связи",
		"Закончим разговор", "ладно, давай, до встречи", "Всё, пока",
	} {
		if !isEndRequest(text) {
			t.Errorf("%q should end the call", text)
		}
	}
	for _, text := range []string{
		"", "Пока не знаю", "Как у тебя дела", "Всё", "Спасибо", "Ну ладно",
		"Расскажи, как сказать до свидания по-французски и по-немецки",
		"Пока ты думаешь, я схожу за чаем", "Найди новости",
	} {
		if isEndRequest(text) {
			t.Errorf("%q should not end the call", text)
		}
	}
}
