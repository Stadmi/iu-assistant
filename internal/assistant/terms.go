package assistant

import "strings"

var localStop = func() map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields("а и или но в во на у к ко с со из от до по о об обо про за для при без над под между через я мы вы ты он она оно они мне меня мой моя мое мои наш ваш его ее их это этот эта эти такой такая такое такие что кто какой какая какое какие как где куда когда почему зачем сколько ли бы же не ни да нет есть был была быть можно нужно нужна нужен нужны надо пожалуйста расскажи расскажите покажи покажите найти найдите узнай узнать подскажи подскажите скажи скажите можешь можете мог могли помоги помогите хочу дай дайте пришли пришлите скинь скиньте ссылку ссылка") {
		out[w] = true
	}
	return out
}()

func localStem(w string) string {
	runes := []rune(w)
	if len(runes) < 4 {
		return w
	}
	for _, r := range runes {
		if r < 'а' || r > 'я' {
			return w
		}
	}
	for _, suffix := range []string{"иями", "ями", "ами", "ого", "ему", "ому", "ыми", "ими", "иях", "ах", "ях", "ых", "их", "ов", "ев", "ей", "ий", "ый", "ой", "ая", "яя", "ое", "ее", "ые", "ие", "ую", "юю", "ия", "ию", "ии", "ом", "ем", "ам", "ям", "а", "я", "у", "ю", "е", "ы", "и", "о", "ь"} {
		if stem, ok := strings.CutSuffix(w, suffix); ok && len([]rune(stem)) >= 3 {
			return stem
		}
	}
	return w
}

func departmentTerm(w string) bool {
	n, ok := strings.CutPrefix(w, "иу")
	return ok && n != "" && strings.Trim(n, "0123456789") == ""
}

// Local terms are used for interpreting dialogue and explicit department codes,
// not for scoring search results.
func localTerms(text string) []string {
	words := strings.Fields(normalized(text))
	out := []string{}
	for i := 0; i < len(words); i++ {
		w := words[i]
		if strings.HasPrefix(w, "iu") {
			w = "иу" + strings.TrimPrefix(w, "iu")
		}
		if w == "иу" && i+1 < len(words) && departmentTerm("иу"+words[i+1]) {
			i++
			w += words[i]
		}
		if w == "связаться" {
			w = "контакты"
		}
		if !localStop[w] && len([]rune(w)) >= 2 {
			out = append(out, localStem(w))
		}
	}
	return out
}
