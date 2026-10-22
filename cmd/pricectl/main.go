// pricectl - консольный клиент к сервису, в основном для демо и ручной проверки.
// Ходит только в HTTP API, в базу напрямую не лезет.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
)

func usage() {
	fmt.Fprint(os.Stderr, `pricectl - клиент сервиса импорта прайса

  pricectl [-server URL] команда [флаги] [аргументы]

Импорт:
  upload [-mode partial|full] [-delimiter auto|,|;|tab] [-encoding auto|utf-8|cp1251] FILE
                         загрузить файл и показать отчет (каталог не меняется)
  show ID                сводка импорта
  report ID [-kind new|changed|unchanged|invalid|missing] [-limit N] [-offset N]
  apply ID [-skip-invalid] [-yes]
                         применить (спросит подтверждение и отправит хэш отчета)
  cancel ID              отменить еще не примененный импорт
  revert ID [-yes]       откатить примененный импорт
  imports [-limit N]     список импортов
  events ID              журнал импорта
  changes ID             что импорт реально поменял

Каталог:
  products [-q TEXT] [-category C] [-active true|false] [-limit N] [-offset N]
  product SKU
  history SKU
  edit SKU [-price P] [-stock N] [-name S] [-category S] [-active=true|false] [-version N]
                         ручная правка (версия берется текущая, если не указать)

Прочее:
  gen [-n N] [-seed S] [-reprice F] [-broken F] [-out FILE]
                         сгенерировать прайс для нагрузочной проверки

Сервер по умолчанию http://localhost:8080 или из PRICE_API.
`)
}

func main() {
	server := os.Getenv("PRICE_API")
	if server == "" {
		server = "http://localhost:8080"
	}
	fs := flag.NewFlagSet("pricectl", flag.ExitOnError)
	fs.Usage = usage
	fs.StringVar(&server, "server", server, "адрес сервиса")
	fs.Parse(os.Args[1:])
	if fs.NArg() == 0 {
		usage()
		os.Exit(2)
	}
	c := &client{base: strings.TrimRight(server, "/")}
	cmd, args := fs.Arg(0), fs.Args()[1:]

	cmds := map[string]func(*client, []string) error{
		"upload":   cmdUpload,
		"show":     cmdShow,
		"report":   cmdReport,
		"apply":    cmdApply,
		"cancel":   cmdCancel,
		"revert":   cmdRevert,
		"imports":  cmdImports,
		"events":   cmdEvents,
		"changes":  cmdChanges,
		"products": cmdProducts,
		"product":  cmdProduct,
		"history":  cmdHistory,
		"edit":     cmdEdit,
		"gen":      cmdGen,
	}
	f, ok := cmds[cmd]
	if !ok {
		fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err := f(c, args); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		var ae *apiError
		if asAPIError(err, &ae) && ae.Status == 409 {
			os.Exit(3) // конфликт - отдельный код, удобно в скриптах
		}
		os.Exit(1)
	}
}

// confirm спрашивает y/N. в скриптах -yes
func confirm(q string) bool {
	fmt.Printf("%s [y/N]: ", q)
	s, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes" || s == "д" || s == "да"
}

// parseArgs - флаги могут идти и до и после позиционных аргументов (stdlib flag так не умеет)
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
