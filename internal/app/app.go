package app

import (
	"fmt"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"pictdraw/internal/processor"
)

func Run() {
	a := app.New()
	w := a.NewWindow("PictDraw - Раскраска по номерам")
	w.Resize(fyne.NewSize(760, 480))

	inputPath := widget.NewLabel("Файл не выбран")
	outputPath := widget.NewEntry()
	outputPath.SetText("output_coloring_a4.svg")
	log := widget.NewMultiLineEntry()
	log.SetMinRowsVisible(12)
	log.Disable()

	appendLog := func(format string, args ...any) {
		log.SetText(log.Text + fmt.Sprintf(format+"\n", args...))
	}

	chooseBtn := widget.NewButton("Выбрать SVG", func() {
		dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
			if err != nil {
				appendLog("Ошибка выбора файла: %v", err)
				return
			}
			if rc == nil {
				return
			}
			inputPath.SetText(rc.URI().Path())
			if outputPath.Text == "" {
				outputPath.SetText(filepath.Join(filepath.Dir(rc.URI().Path()), "output_coloring_a4.svg"))
			}
			_ = rc.Close()
		}, w).Show()
	})

	processBtn := widget.NewButton("Сгенерировать", func() {
		in := inputPath.Text
		if in == "" || in == "Файл не выбран" {
			appendLog("Сначала выберите входной SVG")
			return
		}
		out := outputPath.Text
		if out == "" {
			appendLog("Укажите путь вывода")
			return
		}
		data, err := os.ReadFile(in)
		if err != nil {
			appendLog("Ошибка чтения входа: %v", err)
			return
		}
		res, err := processor.ProcessSVG(data)
		if err != nil {
			appendLog("Ошибка обработки: %v", err)
			return
		}
		if err := os.WriteFile(out, res.SVG, 0o644); err != nil {
			appendLog("Ошибка записи выхода: %v", err)
			return
		}
		appendLog("Успех: сохранено в %s", out)
		appendLog("Найдено цветов: %d", len(res.Legend))
		for _, item := range res.Legend {
			appendLog("  %d -> %s", item.Number, item.Color)
		}
	})

	content := container.NewVBox(
		widget.NewLabel("Загрузите SVG эскиз. Приложение создаст черно-белый A4 SVG с номерами и легендой."),
		container.NewHBox(chooseBtn, inputPath),
		widget.NewForm(widget.NewFormItem("Выходной файл", outputPath)),
		processBtn,
		log,
	)

	w.SetContent(content)
	w.ShowAndRun()
}
