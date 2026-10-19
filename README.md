# price-import

Сервис импорта каталога (прайса) из CSV. Задание 17 «Умный импорт прайса».

## Запуск

```bash
docker compose up --build
```

API на http://localhost:8080, проверка: `curl localhost:8080/healthz`

Тесты (в докере, с -race и настоящим postgres):

```bash
docker compose --profile test run --rm tests
```

TODO: нормально расписать api и как оно работает
