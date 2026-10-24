#!/usr/bin/env bash
# Проверка что гарантии держатся при двух экземплярах сервиса на одной базе.
# Поднимает второй экземпляр (app2), создает товар, строит 10 импортов одного товара
# из одного снимка и одновременно применяет их через оба экземпляра по очереди.
# Ожидается: ровно 1 применен, 9 получили 409 (stale).
set -u
cd "$(dirname "$0")/.."

docker compose up -d --build --wait >/dev/null 2>&1
docker rm -f price-import-app2 >/dev/null 2>&1
docker compose run -d --name price-import-app2 app >/dev/null 2>&1
trap 'docker rm -f price-import-app2 >/dev/null 2>&1' EXIT

ex() { docker compose exec -T app "$@"; }
APP1=http://localhost:8080
APP2=http://price-import-app2:8080
for _ in $(seq 1 30); do
	ex wget -qO- $APP2/healthz >/dev/null 2>&1 && break
	sleep 1
done
echo "экземпляры: $APP1 и $APP2"

upload_id() { ex pricectl upload "$1" | grep -o 'Импорт #[0-9]*' | head -1 | tr -dc '0-9'; }

SKU="RACE-$(date +%s)"
ex sh -c "printf 'sku,name,price\n$SKU,Товар для гонки,100\n' > /tmp/race0.csv"
ex pricectl apply "$(upload_id /tmp/race0.csv)" -yes >/dev/null
echo "создан $SKU"

N=10
ids=()
for i in $(seq 1 $N); do
	ex sh -c "printf 'sku,price\n$SKU,$((200 + i))\n' > /tmp/race$i.csv"
	ids+=("$(upload_id /tmp/race$i.csv)")
done
echo "импорты из одного снимка: ${ids[*]}"

tmp=$(mktemp -d)
for i in $(seq 0 $((N - 1))); do
	srv=$APP1
	[ $((i % 2)) -eq 1 ] && srv=$APP2
	(ex pricectl -server $srv apply "${ids[$i]}" -yes >/dev/null 2>&1; echo $? >"$tmp/$i") &
done
wait
ok=$(grep -lx 0 "$tmp"/* | wc -l | tr -d ' ')
stale=$(grep -lx 3 "$tmp"/* | wc -l | tr -d ' ')
rm -rf "$tmp"
echo "применено: $ok, отклонено как stale: $stale (ждем 1 и $((N - 1)))"
ex pricectl product "$SKU" | sed -n '3p;6p'
ex pricectl history "$SKU"
[ "$ok" = 1 ] && [ "$stale" = $((N - 1)) ]
