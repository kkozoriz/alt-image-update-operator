# PROMPT 1 — сгенерировать PRD.md для проекта ВКР

Ты — GPT-5.5 Pro / GPT-5.5 Thinking. Роль: системный аналитик, архитектор Kubernetes-операторов и Go/Kubebuilder-разработчик.

Задача: на основе контекста ВКР ниже подготовить полноценный PRD для продукта `alt-image-update-operator`. PRD должен описывать весь проект целиком так, чтобы по нему другой ИИ-агент мог создать `tasks.json`, а затем субагенты Codex могли реализовать проект с нуля до рабочего состояния.

Контекст ВКР:
- Тема: «Разработка оператора Kubernetes для автоматизации обновлений безопасности контейнерных образов ALT Linux с пересборкой и декларативным развертыванием».
- Цель: разработать программное средство, которое позволяет декларативно описывать политику обновления контейнерного образа, запускать проверку и пересборку образа, обновлять целевой Kubernetes-ресурс и отражать состояние процесса в статусе пользовательского ресурса.
- Объект разработки: Kubernetes-оператор для автоматизации процесса обновления безопасности контейнерных образов ALT Linux с пересборкой и развертыванием в Kubernetes-кластере.
- В ВКР обосновано: контейнерный образ фиксирует состояние файловой системы на момент сборки; публикация обновлений безопасности в репозитории ALT Linux сама по себе не обновляет уже собранные образы; для применения исправлений требуется новая сборка, публикация образа и обновление Deployment.
- В ВКР выбрана модель Kubernetes-оператора: CRD + reconcile-цикл + Kubernetes Job для конечной операции проверки/пересборки + изменение Deployment через Kubernetes API + запись результата в `status` пользовательского ресурса.
- ALT Linux-контекст: нужно учитывать ветку пакетной базы (`p10`, `p11`, `sisyphus`), базовый образ ALT Linux, обновление индексов пакетов и обновление установленных пакетов внутри сборки.
- Разделы 2–3 ВКР пока являются каркасом, поэтому PRD должен зафиксировать конкретную инженерную спецификацию, которую можно реализовать.

Жесткое требование:
Проект должен быть рабочим продуктом, а не имитацией. Запрещено подменять функциональность заглушками, которые просто печатают успех. Допустимы тестовые doubles только в unit-тестах. В e2e-сценарии должен выполняться реальный Kubernetes-контур: CRD, controller, Job, сборка/пересборка образа через реальный builder, публикация в реестр, обновление Deployment, проверка rollout и статуса.

Рекомендуемая архитектура MVP:
- Язык: Go.
- Фреймворк: Kubebuilder + controller-runtime.
- CRD: `AltImageUpdatePolicy` в API group `security.altlinux.org`, version `v1alpha1`.
- Controller: reconcile по экземплярам `AltImageUpdatePolicy`.
- Check stage: режимы `Always` и `AltAptSimulation`.
  - `Always` используется для воспроизводимого e2e: каждая новая генерация CR или manual trigger запускает rebuild.
  - `AltAptSimulation` запускает Job на ALT Linux image, выполняет `apt-get update` и `apt-get -s dist-upgrade`, controller анализирует логи и решает, есть ли обновления.
- Build stage: реальный Kubernetes Job с Kaniko или BuildKit. По умолчанию использовать Kaniko, чтобы собирать образ без Docker daemon внутри кластера.
- Вход сборки: `Dockerfile` из ConfigMap или Git context. Для MVP обязателен ConfigMap context, Git context может быть follow-up.
- Dockerfile demo должен использовать ALT Linux base image и реально выполнять обновление пакетной базы, например `apt-get update` и безопасную команду обновления пакетов, выбранную в PRD.
- Registry: локальный registry для kind/minikube e2e и опциональный registrySecretRef для внешнего реестра.
- Deployment update: controller обновляет `spec.template.spec.containers[].image` в целевом Deployment для заданного `containerName`, добавляет аннотацию с build id/digest/tag и ожидает rollout.
- Status: conditions, phase, lastCheckTime, lastBuildJobName, lastBuiltImage, lastAppliedImage, observedGeneration, reason/message.
- Idempotency: повторный reconcile не должен создавать дублирующие Job и не должен бесконечно обновлять Deployment.

Сформируй PRD в Markdown. Структура обязательна:
1. Название, краткое описание и цель продукта.
2. Контекст ВКР и проблема.
3. Пользовательские роли и сценарии.
4. Scope MVP и out-of-scope.
5. Функциональные требования FR-001...FR-N.
6. Нефункциональные требования NFR-001...NFR-N.
7. CRD specification: пример YAML, поля `spec`, поля `status`, conditions.
8. State machine/reconcile algorithm: фазы, переходы, идемпотентность, requeue.
9. Check Job design.
10. Build Job design: Kaniko/BuildKit, ConfigMap context, registry, secrets, image tag template.
11. Deployment update design.
12. RBAC и безопасность.
13. Observability: events, logs, metrics минимум.
14. Тестовая стратегия: unit, envtest, e2e в kind/minikube.
15. Demo-сценарий для защиты ВКР: команды, ожидаемые результаты, как доказать корректность.
16. Repository layout.
17. Acceptance criteria: измеримые критерии готовности.
18. Риски и ограничения.
19. Definition of Done для всего продукта.

Правила качества PRD:
- Пиши как инженерную спецификацию, не как реферат.
- Все требования должны быть проверяемыми.
- Не оставляй неоднозначные фразы вроде «реализовать поддержку обновлений» без конкретных входов, выходов и критериев проверки.
- Если информации не хватает, выбери разумное значение по умолчанию и явно пометь его как `Assumption`.
- Не задавай уточняющих вопросов. Сразу выдай готовый `PRD.md`.
- В конце добавь короткий раздел `Implementation notes for Codex agents`, где объясни, какие части реализовывать сначала.

Вывод: только содержимое файла `PRD.md`, без пояснений вокруг.
