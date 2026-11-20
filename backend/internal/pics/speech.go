package pics

import (
	"fmt"
	"strconv"
)

const (
	GreenText      = "信号が青になりました。"
	PreviewText    = "信号の読み上げを試しています。"
	remainingText  = "青まであとおよそ"
	otherBlinkText = "別方向の信号が青点滅しています。"
	soonText       = "まもなく青になる見込みです。"
)

func SpeechParts(c Cue) []string {
	switch c.Kind {
	case "green":
		return []string{GreenText}
	case otherBlinkCue:
		return []string{otherBlinkText}
	case soonCue:
		return []string{soonText}
	case "number":
		return []string{strconv.Itoa(c.Seconds)}
	}
	if c.Seconds <= 0 || c.Seconds > maxSeconds {
		return nil
	}
	if c.Seconds <= 20 || c.Seconds%10 == 0 {
		return []string{remainingText, fmt.Sprintf("%d秒です。", c.Seconds)}
	}
	return []string{remainingText, strconv.Itoa(c.Seconds / 10 * 10), fmt.Sprintf("%d秒です。", c.Seconds%10)}
}

func Vocabulary() []string {
	texts := []string{GreenText, PreviewText, otherBlinkText, soonText, remainingText, "10秒です。"}
	for n := 5; n >= 1; n-- {
		texts = append(texts, strconv.Itoa(n))
	}
	for n := 1; n <= 20; n++ {
		if n != 10 {
			texts = append(texts, fmt.Sprintf("%d秒です。", n))
		}
	}
	for n := 20; n <= 90; n += 10 {
		texts = append(texts, strconv.Itoa(n))
		if n != 20 {
			texts = append(texts, fmt.Sprintf("%d秒です。", n))
		}
	}
	return texts
}
