#!/usr/bin/env bash
# Демо основных сценариев. Запускать на пустой базе:
#   docker compose down -v && docker compose up -d --build && ./scripts/demo.sh
# По умолчанию клиент берется из контейнера app. Если стоит go, можно так:
#   PCTL="go run ./cmd/pricectl" ./scripts/demo.sh
# DEMO_FAST=1 - не ждать Enter между шагами
set -u
cd "$(dirname "$0")/.."

PCTL=${PCTL:-"docker compose exec -T app pricectl"}
pctl() { $PCTL "$@"; }

step() {
	echo
	echo "=================================================================="
	echo "  $*"
	echo "=================================================================="
	if [ -z "${DEMO_FAST:-}" ] && [ -t 0 ]; then
		read -r -p "(Enter - дальше) " _
	fi
}

# upload и вытащить id импорта из вывода
upload() {
	local out
	out=$(pctl upload "$@")
	echo "$out" >&2
	echo "$out" | grep -o 'Импорт #[0-9]*' | head -1 | tr -dc '0-9'
}

if ! pctl products -limit 1 >/dev/null 2>&1; then
	echo "сервис не отвечает, сначала: docker compose up -d --build"
	exit 1
fi
if ! pctl products -limit 1 | grep -q 'всего: 0'; then
	echo "в каталоге уже что-то есть, демо рассчитано на пустую базу:"
	echo "  docker compose down -v && docker compose up -d --build"
	exit 1
fi

step "1. Первая загрузка каталога: все строки новые, каталог пока не меняется"
ID1=$(upload examples/01_initial.csv)
pctl products

step "1a. Подтверждаем. Повторное подтверждение (двойной клик) ничего не делает"
pctl apply "$ID1" -yes
pctl apply "$ID1" -yes
pctl products

step "2. Прайс от поставщика: ; и запятые в ценах, русские заголовки, куча ошибок"
ID2=$(upload examples/02_supplier_update.csv)

step "2a. Применить без явного согласия пропустить битые строки нельзя"
pctl apply "$ID2" -yes || echo "-> отказ, как и должно быть"

step "2b. Применяем с пропуском битых. Предупреждения (цена упала в 10 раз) не блокируют"
pctl apply "$ID2" -skip-invalid -yes
pctl products -category Кухня

step "3. Прайс в cp1251 только с ценами (остальные поля не трогаются)"
ID3=$(upload examples/03_prices_cp1251.csv)

step "3a. Пока отчет ждет подтверждения, менеджер руками меняет цену KT-002"
pctl edit KT-002 -price 1690

step "3b. Подтверждаем старый отчет -> 409, ничего не применилось (даже KT-001)"
pctl apply "$ID3" -yes; echo "exit code: $?"
pctl product KT-001

step "3c. Загружаем тот же файл заново - в отчете уже видно 1690 -> 1790, применяем"
ID3B=$(upload examples/03_prices_cp1251.csv)
pctl apply "$ID3B" -yes

step "4. Полный каталог (mode=full): чего нет в файле - снимается с продажи"
ID4=$(upload -mode full examples/04_full_catalog.csv)
pctl apply "$ID4" -yes
pctl products -active false

step "5. Откат полного импорта: снятые вернулись, KT-004 обратно 599"
pctl revert "$ID4" -yes
pctl products -active false
pctl product KT-004

step "5a. История товара KT-004 со всеми источниками изменений"
pctl history KT-004

step "6. Файл без колонки артикула - отклонен целиком, но остался в истории"
pctl upload examples/05_no_sku_column.csv || true
pctl imports

step "6a. Журнал протухшего импорта"
pctl events "$ID3"

step "7. Большой файл: 100 000 строк (время загрузки+анализа и применения)"
TMP=$(mktemp)
TIMEFORMAT='  -> заняло %R c'
pctl gen -n 100000 -out /tmp/big.csv
time pctl upload /tmp/big.csv >"$TMP"
head -5 "$TMP"
ID7=$(grep -o 'Импорт #[0-9]*' "$TMP" | head -1 | tr -dc '0-9')
time pctl apply "$ID7" -yes

step "7a. Тот же генератор, 30% цен изменено, 1% битых строк"
pctl gen -n 100000 -reprice 0.3 -broken 0.01 -out /tmp/big2.csv
time pctl upload /tmp/big2.csv >"$TMP"
head -5 "$TMP"
ID8=$(grep -o 'Импорт #[0-9]*' "$TMP" | head -1 | tr -dc '0-9')
time pctl apply "$ID8" -skip-invalid -yes
rm -f "$TMP"

echo
echo "Готово. Swagger: http://localhost:8080/docs"
