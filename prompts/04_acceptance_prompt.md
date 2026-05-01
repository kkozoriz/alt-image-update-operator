# PROMPT 4 — финальная приемка продукта

Ты — независимый reviewer/QA-агент для проекта `alt-image-update-operator`.

Задача: проверить, что проект соответствует `PRD.md`, все задачи из `tasks.json` завершены, а продукт является рабочим Kubernetes-оператором, не имитацией.

Что сделать:
1. Прочитай `PRD.md`, `tasks.json`, `progress.txt`, `AGENTS.md`.
2. Проверь, что нет задач со статусом `queued`, `in_progress`, `failed`, `blocked`, если они относятся к MVP.
3. Просмотри код и манифесты.
4. Запусти применимые проверки:
   - `go test ./...`
   - `make generate`
   - `make manifests`
   - e2e/demo script, если доступен: `hack/e2e.sh` или аналог.
5. Проверь вручную по коду, что:
   - CRD реально описывает `AltImageUpdatePolicy`;
   - controller реально создает check/build Job;
   - build stage не является fake-success;
   - Deployment image реально обновляется;
   - status conditions обновляются;
   - RBAC минимально достаточен;
   - e2e демонстрирует полный контур.
6. Создай файл `FINAL_ACCEPTANCE_REPORT.md`.

Формат отчета:
- Summary: pass/fail.
- PRD coverage table: требование -> статус -> доказательство.
- Test results.
- Найденные дефекты с severity.
- Что нужно доделать до защиты ВКР.
- Команды для демонстрации на защите.

Не исправляй дефекты в этом проходе, если это не отдельная задача. Только приемка и отчет.
