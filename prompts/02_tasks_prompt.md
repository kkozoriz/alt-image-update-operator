# PROMPT 2 — разбить PRD.md на tasks.json

Ты — технический лидер и планировщик для Codex-субагентов.

Задача: прочитать `PRD.md` в текущем репозитории и создать файл `tasks.json` с атомарными задачами для реализации проекта `alt-image-update-operator` с нуля до рабочего состояния.

Главная цель: каждая задача должна быть выполнима одним Codex-субагентом в рамках одной сессии. Задачи должны быть достаточно мелкими, проверяемыми и иметь конкретные acceptance criteria и validation commands.

Жесткое требование продукта:
Это должен быть реальный работающий Kubernetes-оператор. Не планируй задачи, которые сводятся к имитации функциональности. Для тестов можно использовать fake clients/envtest, но e2e должен проверять реальный CRD + controller + Kubernetes Job + реальную сборку образа + push в registry + update Deployment.

Что сделать:
1. Прочитай `PRD.md` полностью.
2. Создай `tasks.json` в корне репозитория.
3. Не реализуй код. Только план задач.
4. Также создай или обнови `progress.txt` с записью, что пул задач создан.

Требуемый формат `tasks.json`:
```json
{
  "version": 1,
  "project": "alt-image-update-operator",
  "generated_at": "ISO-8601 UTC timestamp",
  "status_values": ["queued", "in_progress", "done", "blocked", "failed", "skipped"],
  "tasks": [
    {
      "id": "T001",
      "title": "Краткое название",
      "status": "queued",
      "priority": 10,
      "dependencies": [],
      "area": "scaffold|api|controller|jobs|deployment|status|rbac|tests|e2e|docs|ci",
      "estimated_session": "small|medium|large",
      "description": "Что именно надо сделать",
      "files": ["ожидаемые файлы/директории"],
      "acceptance_criteria": ["проверяемый критерий 1", "проверяемый критерий 2"],
      "validation": ["команда или ручная проверка"],
      "implementation_notes": "важные ограничения и подсказки",
      "risk": "low|medium|high",
      "attempts": 0,
      "assigned_to": null,
      "started_at": null,
      "completed_at": null,
      "result_summary": null
    }
  ]
}
```

Правила декомпозиции:
- Начни с bootstrapping репозитория: Kubebuilder scaffold, Go module, Makefile, базовые зависимости.
- Затем CRD/API types и generated manifests.
- Затем controller skeleton и reconcile utilities.
- Затем Job builder/check logic.
- Затем Deployment update logic.
- Затем status/conditions/events.
- Затем RBAC.
- Затем unit/envtest.
- Затем e2e kind/local registry.
- Затем документация и demo scripts.
- В каждой задаче укажи `validation`: например `go test ./...`, `make manifests`, `make generate`, `kubectl ...`, `hack/e2e.sh`.
- Задачи не должны требовать знания, которое есть только в голове пользователя. Все предположения должны быть включены в description или implementation_notes.
- Задачи, которые могут конфликтовать по файлам, ставь последовательно через dependencies.
- Не делай слишком крупные задачи вроде «реализовать контроллер целиком». Разбей на утилиты, reconcile-фазы, статусы, тесты.
- Не создавай задачи «исследовать» без выходного артефакта. Если исследование нужно, укажи конкретный файл с результатом, например `docs/architecture.md`.
- Минимальное количество задач: 20. Максимальное: 60.
- Все `id` должны быть стабильными и сортируемыми: `T001`, `T002`, ...

После записи `tasks.json` проверь, что JSON валиден. Вывод в чате: краткая сводка количества задач по area и список первых 5 задач. Не выводи весь JSON в чат, если файл уже создан.
