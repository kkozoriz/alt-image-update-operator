# PRD.md

# 1. Название, краткое описание и цель продукта

## 1.1. Название

`alt-image-update-operator`

## 1.2. Краткое описание

`alt-image-update-operator` — Kubernetes-оператор для декларативной автоматизации обновлений безопасности контейнерных образов ALT Linux. Оператор позволяет описать пользовательский ресурс `AltImageUpdatePolicy`, который задает:

* какой контейнерный образ необходимо проверять и пересобирать;
* какой Dockerfile/context использовать для сборки;
* какую ветку пакетной базы ALT Linux учитывать;
* куда публиковать новый образ;
* какой Kubernetes `Deployment` и какой контейнер в нем нужно обновить;
* как отражать результат проверки, сборки, публикации и rollout в `status`.

Оператор реализуется на Go с использованием Kubebuilder и controller-runtime. Основная модель работы: `CRD -> reconcile loop -> Check Job -> Build Job -> Registry push -> Deployment patch -> rollout wait -> status update`.

## 1.3. Цель продукта

Разработать рабочий Kubernetes-оператор, который:

1. принимает декларативную политику обновления контейнерного образа ALT Linux;
2. запускает проверку необходимости обновления;
3. при наличии триггера или доступных обновлений запускает реальную пересборку образа через Kaniko;
4. публикует новый образ в контейнерный реестр;
5. обновляет целевой Kubernetes `Deployment`;
6. ожидает завершения rollout;
7. записывает состояние процесса в `status` пользовательского ресурса.

Продукт должен быть пригоден для демонстрации в рамках ВКР и для дальнейшего развития до production-ready решения.

---

# 2. Контекст ВКР и проблема

## 2.1. Контекст

Тема ВКР: «Разработка оператора Kubernetes для автоматизации обновлений безопасности контейнерных образов ALT Linux с пересборкой и декларативным развертыванием».

Контейнерный образ фиксирует состояние файловой системы на момент сборки. Если после сборки в репозитории ALT Linux появляются обновления безопасности, уже собранный образ не изменяется автоматически. Для применения исправлений требуется:

1. обновить индексы пакетной базы внутри процесса сборки;
2. обновить установленные пакеты;
3. собрать новый образ;
4. опубликовать новый образ в реестр;
5. обновить Kubernetes workload;
6. убедиться, что новый workload успешно развернулся.

В Kubernetes такую задачу целесообразно решать через оператор, так как операторная модель позволяет описывать желаемое состояние декларативно и поддерживать фактическое состояние через reconcile-цикл.

## 2.2. Проблема

Без специализированного оператора процесс обновления контейнерных образов ALT Linux остается ручным или фрагментированным:

* администратор должен вручную понять, появились ли обновления;
* вручную запустить пересборку образа;
* вручную опубликовать новый тег;
* вручную изменить `Deployment`;
* вручную проверить rollout;
* вручную зафиксировать результат.

Это повышает риск:

* пропуска критических обновлений;
* ошибочного тега образа;
* обновления не того контейнера;
* неконтролируемого rollout;
* отсутствия аудита действий;
* дрейфа между описанной политикой и фактическим состоянием.

## 2.3. Решение

`alt-image-update-operator` вводит CRD `AltImageUpdatePolicy`, через который пользователь описывает политику обновления. Оператор автоматически выполняет весь цикл:

```text
AltImageUpdatePolicy
  -> Check Job
  -> Build Job
  -> Registry push
  -> Deployment image patch
  -> Rollout observation
  -> Status conditions
```

---

# 3. Пользовательские роли и сценарии

## 3.1. Роли

### Platform Engineer

Отвечает за Kubernetes-кластер, установку оператора, RBAC, registry, namespace isolation и observability.

Основные задачи:

* установить CRD и controller;
* настроить права оператора;
* настроить локальный или внешний registry;
* проверить работу e2e-сценария.

### Application Developer

Отвечает за Dockerfile, исходный build context и приложение.

Основные задачи:

* подготовить Dockerfile на базе ALT Linux;
* описать ConfigMap context;
* создать `AltImageUpdatePolicy`;
* проверить, что после rebuild Deployment использует новый image.

### Security Engineer

Отвечает за политику обновлений и контроль применения исправлений.

Основные задачи:

* настроить check mode;
* проверить, что оператор выполняет `apt-get update`;
* проверить, что оператор выполняет симуляцию `apt-get -s dist-upgrade`;
* проверить статус и события;
* получить доказательство, что Deployment обновлен.

### ВКР Reviewer / Demo User

Проверяет корректность реализации во время защиты.

Основные задачи:

* применить demo-манифесты;
* увидеть создание CRD;
* увидеть Check Job;
* увидеть Build Job;
* увидеть публикацию образа в registry;
* увидеть изменение Deployment;
* увидеть успешный rollout;
* увидеть `status` ресурса.

## 3.2. Основные пользовательские сценарии

### UC-001. Принудительная пересборка образа для демонстрации

Пользователь создает `AltImageUpdatePolicy` с `spec.check.mode: Always`.

Оператор должен:

1. увидеть новую генерацию ресурса;
2. создать Build Job;
3. собрать образ через Kaniko;
4. опубликовать образ в registry;
5. обновить целевой Deployment;
6. дождаться rollout;
7. записать успешный статус.

Этот сценарий обязателен для e2e и защиты ВКР.

### UC-002. Проверка обновлений через ALT apt simulation

Пользователь создает `AltImageUpdatePolicy` с `spec.check.mode: AltAptSimulation`.

Оператор должен:

1. создать Check Job на ALT Linux image;
2. выполнить `apt-get update`;
3. выполнить `apt-get -s dist-upgrade`;
4. получить логи Check Job;
5. определить, есть ли обновления;
6. при наличии обновлений запустить Build Job;
7. при отсутствии обновлений записать состояние `UpToDate`.

### UC-003. Обновление конкретного контейнера в Deployment

Пользователь указывает:

```yaml
spec:
  targetRef:
    kind: Deployment
    name: demo-app
  containerName: app
```

Оператор должен изменить только контейнер `app` в `spec.template.spec.containers[]` целевого Deployment. Остальные контейнеры должны остаться без изменений.

### UC-004. Повторный reconcile без дублирования Job

При повторном reconcile без изменения `generation`, trigger token или завершившегося состояния оператор не должен создавать новые Check Job или Build Job.

### UC-005. Ручной повторный запуск

Пользователь меняет `spec.trigger.manualToken`.

Оператор должен считать это новым запуском политики и выполнить check/build pipeline заново.

---

# 4. Scope MVP и out-of-scope

## 4.1. Scope MVP

MVP должен включать:

* Go-проект на Kubebuilder.
* CRD `AltImageUpdatePolicy`.
* API group: `security.altlinux.org`.
* Version: `v1alpha1`.
* Controller на controller-runtime.
* Reconcile по ресурсам `AltImageUpdatePolicy`.
* Check mode:

    * `Always`;
    * `AltAptSimulation`.
* Build stage:

    * Kubernetes Job;
    * Kaniko по умолчанию;
    * реальная сборка образа;
    * push в registry.
* Build context:

    * обязательный источник — ConfigMap;
    * Dockerfile из ConfigMap key.
* ALT Linux build:

    * Dockerfile должен использовать ALT Linux base image;
    * Dockerfile должен выполнять `apt-get update`;
    * Dockerfile должен выполнять безопасное обновление пакетов.
* Registry:

    * локальный registry для kind/minikube e2e;
    * опциональный `registrySecretRef`.
* Deployment update:

    * patch `Deployment.spec.template.spec.containers[].image`;
    * добавление build-аннотаций;
    * ожидание rollout.
* Status:

    * `phase`;
    * `conditions`;
    * `observedGeneration`;
    * `lastCheckTime`;
    * `lastBuildJobName`;
    * `lastBuiltImage`;
    * `lastAppliedImage`;
    * `lastRolloutTime`;
    * `reason`;
    * `message`.
* RBAC для:

    * CRD;
    * Jobs;
    * Pods/log;
    * Deployments;
    * Events;
    * ConfigMaps;
    * Secrets read для registry secret.
* Unit tests.
* envtest tests.
* e2e tests в kind или minikube.
* Demo-манифесты для защиты.

## 4.2. Out-of-scope для MVP

В MVP не входят:

* Git build context.
* Cron/schedule-based запуск.
* Поддержка StatefulSet, DaemonSet, Rollout, Argo Rollouts.
* Проверка CVE database.
* Интеграция с внешними security scanners.
* Cosign signing.
* SBOM generation.
* Multi-arch build.
* Build cache tuning.
* Admission webhook.
* Conversion webhook.
* Production-grade retry policy с backoff history.
* Поддержка нескольких контейнеров в одном policy.
* Автоматическое создание локального registry.
* Автоматическая настройка kind containerd registry mirror.
* Автоматический rollback при неуспешном rollout.
* Управление lifecycle старых тегов в registry.
* Секреты для Git.
* Поддержка private base images отдельно от registry push secret.

## 4.3. Assumptions

### Assumption A-001

Для MVP целевой workload — только Kubernetes `Deployment` из API group `apps/v1`.

### Assumption A-002

Для e2e используется kind с локальным registry, доступным из кластера и из node runtime.

### Assumption A-003

По умолчанию используется Kaniko executor image:

```text
gcr.io/kaniko-project/executor:latest
```

Для воспроизводимости в реализации рекомендуется закрепить конкретный digest или tag, но PRD допускает параметризацию через `spec.build.builderImage`.

### Assumption A-004

ALT Linux base image для demo Dockerfile задается как параметр `spec.alt.baseImage`.

Значение по умолчанию для demo:

```text
alt:p10
```

Если в конкретной среде требуется другой registry path для ALT image, demo-манифесты должны позволять заменить это значение без изменения кода оператора.

### Assumption A-005

Безопасная команда обновления пакетов в demo Dockerfile:

```dockerfile
RUN apt-get update && apt-get -y dist-upgrade && apt-get clean
```

Обоснование: `apt-get -y dist-upgrade` реально применяет доступные обновления в образе во время сборки, а `apt-get clean` уменьшает размер итогового образа. Для runtime demo-приложения должен использоваться безопасный простой процесс, например HTTP server или shell loop.

### Assumption A-006

Контроллер не должен сам определять «security-only» обновления в MVP. Он проверяет наличие доступных обновлений через apt simulation и пересобирает образ, если симуляция показывает изменения пакетов.

---

# 5. Функциональные требования

## FR-001. CRD

Система должна предоставлять CRD `AltImageUpdatePolicy`:

```text
plural: altimageupdatepolicies
singular: altimageupdatepolicy
kind: AltImageUpdatePolicy
shortNames:
  - aiup
apiGroup: security.altlinux.org
version: v1alpha1
scope: Namespaced
```

Критерий проверки:

```bash
kubectl get crd altimageupdatepolicies.security.altlinux.org
kubectl api-resources | grep AltImageUpdatePolicy
```

## FR-002. Создание reconcile loop

Контроллер должен отслеживать создание, изменение и удаление ресурсов `AltImageUpdatePolicy`.

Критерий проверки:

* после создания CR оператор записывает `status.observedGeneration`;
* оператор создает нужные Jobs или обновляет status в зависимости от `spec`.

## FR-003. Поддержка check mode `Always`

При `spec.check.mode: Always` оператор должен запускать Build Job для каждой новой генерации CR или нового `spec.trigger.manualToken`.

Критерии проверки:

* создание нового CR запускает Build Job;
* изменение `spec.trigger.manualToken` запускает новый Build Job;
* повторный reconcile без изменения generation/manualToken не запускает новый Build Job.

## FR-004. Поддержка check mode `AltAptSimulation`

При `spec.check.mode: AltAptSimulation` оператор должен создать Check Job, который выполняет:

```bash
apt-get update
apt-get -s dist-upgrade
```

Критерии проверки:

* создается Kubernetes Job с ALT Linux image;
* Job завершается с `Complete=True`;
* controller читает logs pod'а Check Job;
* если logs показывают доступные обновления, создается Build Job;
* если logs показывают отсутствие обновлений, Build Job не создается, status получает phase `UpToDate`.

## FR-005. Анализ логов Check Job

Контроллер должен анализировать stdout/stderr Check Job и определять наличие обновлений.

MVP-алгоритм:

* если в логах найден паттерн, соответствующий установке/обновлению пакетов, check result = `UpdatesAvailable`;
* если найдено явное отсутствие изменений, check result = `NoUpdates`;
* если Job завершился ошибкой или логи не удалось получить, check result = `CheckFailed`.

Минимальный набор паттернов:

```text
Inst 
The following packages will be upgraded
The following NEW packages will be installed
The following packages will be REMOVED
```

`NoUpdates` допускается определять по отсутствию upgrade/install/remove паттернов при успешном exit code `0`.

Критерии проверки:

* unit-тесты для parser'а логов;
* envtest/e2e показывает переход в `UpToDate` или `Building`.

## FR-006. Создание Build Job

Оператор должен создавать Kubernetes Job для сборки образа через Kaniko.

Критерии проверки:

* Job имеет ownerReference на `AltImageUpdatePolicy`;
* Job монтирует ConfigMap build context;
* Job запускает Kaniko executor;
* Job публикует image в `spec.build.outputImage`;
* Job завершает работу с `Complete=True`.

## FR-007. ConfigMap build context

Для MVP оператор должен поддерживать build context из ConfigMap.

Минимальная структура:

```yaml
spec:
  build:
    context:
      type: ConfigMap
      configMapRef:
        name: demo-app-context
        dockerfileKey: Dockerfile
```

Критерии проверки:

* Build Job монтирует указанный ConfigMap в `/workspace`;
* Kaniko использует `--context=dir:///workspace`;
* Kaniko использует `--dockerfile=/workspace/<dockerfileKey>`.

## FR-008. Поддержка Dockerfile ALT Linux

Demo Dockerfile должен использовать ALT Linux base image и выполнять обновление пакетов.

Минимальный demo Dockerfile:

```dockerfile
FROM alt:p10
RUN apt-get update && apt-get -y dist-upgrade && apt-get clean
RUN echo '#!/bin/sh' > /usr/local/bin/demo-app \
    && echo 'while true; do echo "alt demo app: $(date)"; sleep 30; done' >> /usr/local/bin/demo-app \
    && chmod +x /usr/local/bin/demo-app
CMD ["/usr/local/bin/demo-app"]
```

Критерии проверки:

* Build Job реально выполняет Dockerfile;
* в логах Build Job видны команды `apt-get update` и `dist-upgrade`;
* образ успешно публикуется в registry.

## FR-009. Публикация образа в registry

Оператор должен поддерживать публикацию итогового образа в registry через Kaniko.

Поля:

```yaml
spec:
  build:
    outputImage: localhost:5001/alt/demo-app
    tagTemplate: "{{ .Name }}-{{ .Generation }}-{{ .Timestamp }}"
```

Критерии проверки:

* итоговый image reference содержит repository и tag;
* Deployment обновляется на этот image reference;
* image доступен node runtime и может быть pulled.

## FR-010. Registry secret

Оператор должен поддерживать опциональный secret для push в private registry:

```yaml
spec:
  build:
    registrySecretRef:
      name: registry-auth
```

Критерии проверки:

* при наличии `registrySecretRef` Build Job монтирует secret в Kaniko docker config path;
* при отсутствии secret Build Job запускается без registry auth volume.

## FR-011. Генерация image tag

Оператор должен генерировать уникальный tag для каждой новой сборки.

Поддерживаемые template variables:

```text
.Name
.Namespace
.Generation
.ObservedGeneration
.ManualTokenHash
.Timestamp
.ShortUID
```

Default `tagTemplate`:

```text
{{ .Name }}-gen{{ .Generation }}-{{ .Timestamp }}
```

Формат `Timestamp`:

```text
yyyyMMddHHmmss
```

Критерии проверки:

* tag соответствует Docker tag constraints;
* повторный reconcile одного и того же запуска использует тот же build id и image tag;
* новый manualToken создает новый tag.

## FR-012. Идемпотентность Build Job

Оператор не должен создавать дублирующие Build Job для одного и того же build id.

Критерии проверки:

* при повторном reconcile количество Build Job не увеличивается;
* если Job уже существует и не завершен, оператор ожидает его;
* если Job уже завершен успешно, оператор переходит к Deployment update;
* если Job завершен неуспешно, оператор выставляет condition `BuildFailed`.

## FR-013. Обновление Deployment

Оператор должен обновлять указанный контейнер в целевом Deployment.

Поля:

```yaml
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: demo-app
  containerName: app
```

Критерии проверки:

* изменяется только контейнер с именем `app`;
* если контейнер не найден, status condition `TargetNotFound=True`;
* если Deployment не найден, status condition `TargetNotFound=True`;
* если patch успешен, status phase становится `RollingOut`.

## FR-014. Deployment annotations

Оператор должен добавлять или обновлять аннотации в `Deployment.spec.template.metadata.annotations`.

Минимальные аннотации:

```text
security.altlinux.org/last-build-id
security.altlinux.org/last-built-image
security.altlinux.org/last-applied-at
security.altlinux.org/policy-name
security.altlinux.org/policy-namespace
```

Критерии проверки:

* аннотации присутствуют после patch;
* изменение аннотаций вызывает новый ReplicaSet;
* значения соответствуют текущему build id и image.

## FR-015. Ожидание rollout

После patch Deployment оператор должен ожидать успешного rollout.

MVP-критерии успешного rollout:

* `deployment.status.observedGeneration >= deployment.metadata.generation`;
* condition `Available=True`;
* condition `Progressing=True`;
* `status.updatedReplicas == spec.replicas`;
* `status.readyReplicas == spec.replicas`;
* `status.unavailableReplicas` отсутствует или равен `0`.

Критерии проверки:

* после обновления Deployment status CR получает phase `Succeeded`;
* `status.lastAppliedImage` равен image в Deployment;
* `kubectl rollout status deployment/<name>` завершается успешно.

## FR-016. Rollout timeout

Оператор должен поддерживать timeout ожидания rollout.

Default:

```yaml
spec:
  rollout:
    timeoutSeconds: 300
```

Критерии проверки:

* если rollout не завершен до timeout, phase становится `Failed`;
* condition `RolloutFailed=True`;
* message содержит причину timeout.

## FR-017. Status phase

Оператор должен поддерживать следующие значения `status.phase`:

```text
Pending
Checking
UpToDate
Building
Publishing
Applying
RollingOut
Succeeded
Failed
```

Критерии проверки:

* phase меняется согласно state machine;
* phase не остается пустым после первого reconcile.

## FR-018. Status conditions

Оператор должен поддерживать conditions:

```text
Ready
CheckCompleted
UpdatesAvailable
BuildCompleted
ImagePublished
DeploymentUpdated
RolloutCompleted
UpToDate
Failed
```

Критерии проверки:

* каждая condition содержит `type`, `status`, `reason`, `message`, `lastTransitionTime`, `observedGeneration`;
* при success `Ready=True`;
* при failure `Failed=True`, `Ready=False`.

## FR-019. Status timestamps

Оператор должен записывать timestamps:

```yaml
status:
  lastCheckTime:
  lastBuildStartTime:
  lastBuildCompletionTime:
  lastApplyTime:
  lastRolloutTime:
```

Критерии проверки:

* поля заполняются на соответствующих стадиях;
* timestamps имеют формат Kubernetes `metav1.Time`.

## FR-020. Status job names

Оператор должен записывать имена последних Job:

```yaml
status:
  lastCheckJobName:
  lastBuildJobName:
```

Критерии проверки:

* значения соответствуют реально созданным Job;
* Job можно найти через `kubectl get job <name>`.

## FR-021. Status image fields

Оператор должен записывать:

```yaml
status:
  lastBuiltImage:
  lastAppliedImage:
```

Критерии проверки:

* `lastBuiltImage` устанавливается после успешной Build Job;
* `lastAppliedImage` устанавливается после успешного Deployment patch;
* после success оба поля равны ожидаемому image reference.

## FR-022. Events

Оператор должен создавать Kubernetes Events для ключевых действий:

```text
CheckStarted
CheckCompleted
CheckFailed
BuildStarted
BuildCompleted
BuildFailed
DeploymentUpdateStarted
DeploymentUpdated
RolloutCompleted
RolloutFailed
PolicySucceeded
PolicyFailed
```

Критерии проверки:

```bash
kubectl describe altimageupdatepolicy <name>
```

должен показывать события оператора.

## FR-023. Logs controller

Контроллер должен логировать структурированные события reconcile.

Минимальные поля:

```text
namespace
name
generation
phase
buildID
checkJobName
buildJobName
targetDeployment
targetContainer
image
```

Критерии проверки:

* logs operator pod содержат эти поля;
* ошибки логируются с reason.

## FR-024. Metrics

Оператор должен предоставлять Prometheus metrics через controller-runtime metrics endpoint.

Минимальные кастомные метрики:

```text
alt_image_update_reconcile_total
alt_image_update_check_total
alt_image_update_build_total
alt_image_update_deployment_update_total
alt_image_update_rollout_total
alt_image_update_failure_total
```

Labels:

```text
namespace
policy
result
reason
```

Критерии проверки:

* endpoint `/metrics` содержит метрики;
* unit или envtest проверяет регистрацию collector'ов, если применимо.

## FR-025. Owner references и cleanup

Check Job и Build Job должны иметь ownerReference на `AltImageUpdatePolicy`.

Критерии проверки:

* при удалении CR Kubernetes garbage collector удаляет дочерние Jobs;
* Jobs имеют labels для поиска.

Минимальные labels:

```text
app.kubernetes.io/name=alt-image-update-operator
app.kubernetes.io/managed-by=alt-image-update-operator
security.altlinux.org/policy-name=<policy>
security.altlinux.org/policy-uid=<uid>
security.altlinux.org/build-id=<buildID>
security.altlinux.org/job-type=check|build
```

## FR-026. TTL для Jobs

Оператор должен поддерживать TTL для завершенных Jobs.

Default:

```yaml
spec:
  jobTemplate:
    ttlSecondsAfterFinished: 600
```

Критерии проверки:

* Jobs создаются с `ttlSecondsAfterFinished`;
* значение можно переопределить через spec.

## FR-027. ServiceAccount для Jobs

Оператор должен поддерживать настройку ServiceAccount для Check Job и Build Job.

Default:

```yaml
spec:
  jobTemplate:
    serviceAccountName: default
```

Критерии проверки:

* Job pod использует заданный ServiceAccount;
* если поле не задано, используется `default`.

## FR-028. Namespace behavior

`AltImageUpdatePolicy` является namespaced resource.

Правила MVP:

* Check Job создается в namespace CR;
* Build Job создается в namespace CR;
* ConfigMap ищется в namespace CR;
* target Deployment ищется в namespace CR;
* registrySecretRef ищется в namespace CR.

Критерии проверки:

* e2e использует один namespace;
* controller не обращается к ресурсам в других namespace для MVP.

## FR-029. Валидация CRD

CRD OpenAPI schema должна валидировать обязательные поля:

```text
spec.alt.branch
spec.alt.baseImage
spec.check.mode
spec.build.context.type
spec.build.context.configMapRef.name
spec.build.context.configMapRef.dockerfileKey
spec.build.outputImage
spec.targetRef.apiVersion
spec.targetRef.kind
spec.targetRef.name
spec.containerName
```

Критерии проверки:

* `kubectl apply` отклоняет CR без обязательных полей;
* enum validation работает для `check.mode` и `alt.branch`.

## FR-030. Поддержка ALT branch

CRD должен поддерживать:

```text
p10
p11
sisyphus
```

Поле:

```yaml
spec:
  alt:
    branch: p10
```

Критерии проверки:

* CR с другим значением отклоняется схемой;
* значение branch доступно controller'у и передается в Job через env.

## FR-031. Поддержка builder selection

MVP должен поддерживать builder type:

```text
Kaniko
```

CRD может предусмотреть follow-up значение:

```text
BuildKit
```

но controller в MVP обязан реализовать только Kaniko.

Критерии проверки:

* `spec.build.builder: Kaniko` работает;
* `BuildKit` либо отклоняется схемой в MVP, либо приводит к clear status `UnsupportedBuilder`.

Рекомендуемое MVP-решение: enum только `Kaniko`.

## FR-032. Ошибка при некорректном Dockerfile key

Если `dockerfileKey` отсутствует в ConfigMap, Build Job должен завершиться ошибкой или controller должен заранее выставить failure.

MVP-поведение:

* controller проверяет наличие ConfigMap и key до создания Build Job;
* если key отсутствует, phase `Failed`;
* condition `Failed=True`, reason `DockerfileKeyNotFound`.

Критерии проверки:

* envtest проверяет отсутствие key;
* Build Job не создается при заранее обнаруженной ошибке.

## FR-033. Ошибка при отсутствии ConfigMap

Если ConfigMap context отсутствует, оператор должен выставить failure без создания Build Job.

Критерии проверки:

* phase `Failed`;
* reason `BuildContextNotFound`;
* event `PolicyFailed`.

## FR-034. Ошибка при отсутствии Deployment

Если target Deployment отсутствует, оператор должен выставить failure.

Критерии проверки:

* phase `Failed`;
* reason `TargetDeploymentNotFound`;
* Build Job не запускается, если target проверяется до build;
* либо Build Job может быть уже завершен, но apply stage должен завершиться failure.

MVP-решение: controller проверяет target Deployment до Check/Build, чтобы избежать ненужной сборки.

## FR-035. Ошибка при отсутствии containerName

Если target Deployment существует, но контейнер с именем `spec.containerName` отсутствует, оператор должен выставить failure.

Критерии проверки:

* phase `Failed`;
* reason `TargetContainerNotFound`;
* Deployment не изменяется.

## FR-036. Повторный запуск после failure

Пользователь должен иметь возможность повторно запустить policy после failure путем изменения `spec.trigger.manualToken`.

Критерии проверки:

* после изменения token operator запускает pipeline заново;
* old failure condition обновляется;
* новая успешная попытка переводит Ready в `True`.

## FR-037. Стабильный build id

Для одной попытки operator должен использовать стабильный build id.

Build id должен зависеть от:

```text
policy UID
metadata.generation
spec.trigger.manualToken
```

Допускаемый формат:

```text
b-<short-uid>-g<generation>-<manual-token-hash>
```

Если `manualToken` пустой:

```text
b-<short-uid>-g<generation>
```

Критерии проверки:

* повторный reconcile генерирует тот же build id;
* изменение generation меняет build id;
* изменение manualToken меняет build id.

## FR-038. Поддержка successful no-op

Если `AltAptSimulation` определяет отсутствие обновлений, operator должен завершить reconcile без Build Job и выставить:

```yaml
status:
  phase: UpToDate
conditions:
  - type: Ready
    status: "True"
    reason: UpToDate
  - type: UpToDate
    status: "True"
```

Критерии проверки:

* Build Job не создан;
* Deployment не изменен;
* Ready=True.

## FR-039. Finalizers

Для MVP finalizer не обязателен.

Если finalizer реализован, он не должен блокировать удаление CR при отсутствии внешних ресурсов, требующих ручной очистки.

Критерий проверки:

* `kubectl delete altimageupdatepolicy <name>` завершается без зависания.

## FR-040. CLI-free operation

Оператор не должен использовать `kubectl` внутри controller или Job.

Критерии проверки:

* controller использует Kubernetes API через controller-runtime client;
* Check Job использует только apt commands;
* Build Job использует Kaniko executor.

---

# 6. Нефункциональные требования

## NFR-001. Реальность функциональности

Функциональность не должна подменяться заглушками.

Запрещено:

* создавать fake Job, который просто печатает success;
* пропускать фактическую сборку образа;
* пропускать push в registry;
* обновлять status без проверки состояния Job;
* обновлять Deployment без реального image reference.

Допустимо:

* использовать test doubles только в unit tests;
* использовать fake client только в unit tests;
* использовать envtest для API-level tests;
* использовать kind/minikube для e2e.

## NFR-002. Язык и стек

Проект должен быть реализован на Go.

Минимальный стек:

```text
Go
Kubebuilder
controller-runtime
client-go
k8s.io/api
k8s.io/apimachinery
```

## NFR-003. Совместимость Kubernetes

MVP должен поддерживать Kubernetes версии:

```text
1.28+
```

Assumption: e2e выполняется на актуальной версии kind/minikube, совместимой с Kubernetes `1.28+`.

## NFR-004. Надежность reconcile

Reconcile должен быть идемпотентным.

Критерии:

* повторный reconcile не создает дублирующие Jobs;
* повторный reconcile не патчит Deployment бесконечно;
* status обновляется только при фактическом изменении;
* ошибки не приводят к бесконечному tight loop.

## NFR-005. Requeue policy

Оператор должен использовать ограниченный requeue.

Default:

```text
Job polling requeue: 5s
Rollout polling requeue: 5s
Error retry requeue: controller-runtime default exponential backoff
```

Критерии:

* при активном Job reconcile возвращает `RequeueAfter: 5s`;
* при ожидании rollout reconcile возвращает `RequeueAfter: 5s`;
* при терминальной ошибке без изменения spec бесконечный retry не выполняет новую сборку.

## NFR-006. Безопасность credentials

Registry credentials должны использоваться только через Kubernetes Secret.

Критерии:

* secret не пишется в logs;
* secret не копируется в status;
* secret монтируется только в Build Job pod;
* controller не печатает содержимое secret.

## NFR-007. Минимизация прав

RBAC оператора должен выдавать только необходимые права.

Критерии:

* нет cluster-admin;
* namespaced resources используются через Role, где возможно;
* CRD permissions выдаются через controller-manager ClusterRole согласно Kubebuilder default.

## NFR-008. Observability

Оператор должен обеспечивать минимальный уровень наблюдаемости:

* Kubernetes Events;
* structured logs;
* status conditions;
* metrics endpoint.

## NFR-009. Производительность

MVP рассчитан на малое число политик.

Минимальный критерий:

* 10 одновременно существующих `AltImageUpdatePolicy` не должны вызывать падение controller-manager;
* reconcile одной policy не должен блокировать reconcile других policy на время выполнения Job.

## NFR-010. Отказоустойчивость controller restart

После перезапуска controller-manager оператор должен продолжить наблюдение за уже созданными Check/Build Jobs.

Критерии:

* Job names детерминированы по build id;
* controller может найти существующий Job по name/labels;
* status восстанавливается на основании Job и Deployment state.

## NFR-011. Deterministic naming

Имена Jobs должны быть детерминированы и соответствовать DNS-1123.

Формат:

```text
<policy-name>-<build-id>-check
<policy-name>-<build-id>-build
```

Если длина превышает Kubernetes limit, имя должно быть сокращено с hash suffix.

## NFR-012. Документируемость

Репозиторий должен содержать:

* README;
* PRD.md;
* docs/demo.md;
* docs/troubleshooting.md;
* config/samples;
* e2e instructions.

## NFR-013. Проверяемость

Каждое функциональное требование должно иметь проверку через:

* unit test;
* envtest;
* e2e test;
* manual demo command.

## NFR-014. Portability demo

Demo должен запускаться в kind или minikube без внешнего коммерческого registry.

Критерии:

* локальный registry используется для e2e;
* registry address задается через manifests или env var;
* demo не требует Docker daemon внутри Kubernetes pod.

## NFR-015. Maintainability

Код должен быть разделен на пакеты:

* API types;
* controller;
* job factory;
* check log parser;
* deployment patcher;
* image tag generator;
* status helper;
* metrics.

Критерий:

* parser и tag generator покрыты unit tests без Kubernetes API.

---

# 7. CRD specification

## 7.1. API identity

```text
Group: security.altlinux.org
Version: v1alpha1
Kind: AltImageUpdatePolicy
Plural: altimageupdatepolicies
Scope: Namespaced
Short name: aiup
```

## 7.2. Пример YAML

```yaml
apiVersion: security.altlinux.org/v1alpha1
kind: AltImageUpdatePolicy
metadata:
  name: demo-app-policy
  namespace: alt-update-demo
spec:
  alt:
    branch: p10
    baseImage: alt:p10

  trigger:
    manualToken: "demo-run-001"

  check:
    mode: Always

  build:
    builder: Kaniko
    builderImage: gcr.io/kaniko-project/executor:latest
    outputImage: localhost:5001/alt/demo-app
    tagTemplate: "{{ .Name }}-gen{{ .Generation }}-{{ .Timestamp }}"
    context:
      type: ConfigMap
      configMapRef:
        name: demo-app-context
        dockerfileKey: Dockerfile
    registrySecretRef:
      name: registry-auth

  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: demo-app

  containerName: app

  rollout:
    timeoutSeconds: 300

  jobTemplate:
    serviceAccountName: default
    ttlSecondsAfterFinished: 600
    backoffLimit: 0
    activeDeadlineSeconds: 900
```

Для локального registry без авторизации поле `registrySecretRef` должно быть опущено:

```yaml
spec:
  build:
    registrySecretRef: null
```

## 7.3. Поля `spec`

### 7.3.1. `spec.alt`

```yaml
alt:
  branch: p10
  baseImage: alt:p10
```

| Поле        |         Тип | Обязательное | Описание                                                                      |
| ----------- | ----------: | -----------: | ----------------------------------------------------------------------------- |
| `branch`    | string enum |           Да | Ветка пакетной базы ALT Linux. Допустимые значения: `p10`, `p11`, `sisyphus`. |
| `baseImage` |      string |           Да | Базовый образ ALT Linux, используемый Check Job и demo Dockerfile.            |

В MVP controller не переписывает Dockerfile автоматически на основе `baseImage`. Это значение используется:

* как image для Check Job;
* как env `ALT_BASE_IMAGE` для Job;
* как документированная связь policy с ALT base.

### 7.3.2. `spec.trigger`

```yaml
trigger:
  manualToken: "demo-run-001"
```

| Поле          |    Тип | Обязательное | Описание                                                                                                   |
| ------------- | -----: | -----------: | ---------------------------------------------------------------------------------------------------------- |
| `manualToken` | string |          Нет | Пользовательский token для ручного повторного запуска pipeline. Изменение значения создает новый build id. |

Пустой `manualToken` допустим.

### 7.3.3. `spec.check`

```yaml
check:
  mode: Always
```

| Поле   |         Тип | Обязательное | Описание                                                           |
| ------ | ----------: | -----------: | ------------------------------------------------------------------ |
| `mode` | string enum |           Да | Режим проверки. Допустимые значения: `Always`, `AltAptSimulation`. |

Поведение:

* `Always`: сразу перейти к build stage;
* `AltAptSimulation`: создать Check Job и анализировать логи apt simulation.

### 7.3.4. `spec.build`

```yaml
build:
  builder: Kaniko
  builderImage: gcr.io/kaniko-project/executor:latest
  outputImage: localhost:5001/alt/demo-app
  tagTemplate: "{{ .Name }}-gen{{ .Generation }}-{{ .Timestamp }}"
  context:
    type: ConfigMap
    configMapRef:
      name: demo-app-context
      dockerfileKey: Dockerfile
  registrySecretRef:
    name: registry-auth
```

| Поле                |                  Тип | Обязательное | Описание                                                 |
| ------------------- | -------------------: | -----------: | -------------------------------------------------------- |
| `builder`           |          string enum |           Да | Builder. MVP поддерживает только `Kaniko`.               |
| `builderImage`      |               string |          Нет | Image Kaniko executor. Если пусто, используется default. |
| `outputImage`       |               string |           Да | Repository итогового image без tag или с tag.            |
| `tagTemplate`       |               string |          Нет | Template для tag. Если пусто, используется default.      |
| `context`           |               object |           Да | Источник build context.                                  |
| `registrySecretRef` | LocalObjectReference |          Нет | Secret с Docker config для push в registry.              |

Правило для `outputImage`:

* если `outputImage` содержит tag, controller должен заменить tag на сгенерированный tag;
* если `outputImage` не содержит tag, controller должен добавить сгенерированный tag;
* digest-based output в MVP не поддерживается как input, но может быть записан в status, если Kaniko digest доступен.

### 7.3.5. `spec.build.context`

```yaml
context:
  type: ConfigMap
  configMapRef:
    name: demo-app-context
    dockerfileKey: Dockerfile
```

| Поле                         |         Тип | Обязательное | Описание                                     |
| ---------------------------- | ----------: | -----------: | -------------------------------------------- |
| `type`                       | string enum |           Да | MVP поддерживает только `ConfigMap`.         |
| `configMapRef.name`          |      string |           Да | Имя ConfigMap с build context.               |
| `configMapRef.dockerfileKey` |      string |           Да | Key внутри ConfigMap, содержащий Dockerfile. |

### 7.3.6. `spec.targetRef`

```yaml
targetRef:
  apiVersion: apps/v1
  kind: Deployment
  name: demo-app
```

| Поле         |    Тип | Обязательное | Описание                           |
| ------------ | -----: | -----------: | ---------------------------------- |
| `apiVersion` | string |           Да | Для MVP только `apps/v1`.          |
| `kind`       | string |           Да | Для MVP только `Deployment`.       |
| `name`       | string |           Да | Имя Deployment в namespace policy. |

### 7.3.7. `spec.containerName`

```yaml
containerName: app
```

Имя контейнера в `Deployment.spec.template.spec.containers[]`, который должен быть обновлен.

### 7.3.8. `spec.rollout`

```yaml
rollout:
  timeoutSeconds: 300
```

| Поле             | Тип | Обязательное | Описание                                  |
| ---------------- | --: | -----------: | ----------------------------------------- |
| `timeoutSeconds` | int |          Нет | Timeout ожидания rollout. Default: `300`. |

### 7.3.9. `spec.jobTemplate`

```yaml
jobTemplate:
  serviceAccountName: default
  ttlSecondsAfterFinished: 600
  backoffLimit: 0
  activeDeadlineSeconds: 900
```

| Поле                      |    Тип | Обязательное | Описание                                                 |
| ------------------------- | -----: | -----------: | -------------------------------------------------------- |
| `serviceAccountName`      | string |          Нет | ServiceAccount для Check/Build Jobs. Default: `default`. |
| `ttlSecondsAfterFinished` |    int |          Нет | TTL завершенных Jobs. Default: `600`.                    |
| `backoffLimit`            |    int |          Нет | Kubernetes Job backoffLimit. Default: `0`.               |
| `activeDeadlineSeconds`   |    int |          Нет | Максимальное время выполнения Job. Default: `900`.       |

## 7.4. Поля `status`

```yaml
status:
  observedGeneration: 1
  phase: Succeeded
  buildID: b-abc123-g1-6f4d2a
  currentRunKey: "1:6f4d2a"
  lastCheckTime: "2026-05-01T10:00:00Z"
  lastBuildStartTime: "2026-05-01T10:00:15Z"
  lastBuildCompletionTime: "2026-05-01T10:02:10Z"
  lastApplyTime: "2026-05-01T10:02:20Z"
  lastRolloutTime: "2026-05-01T10:02:55Z"
  lastCheckJobName: demo-app-policy-b-abc123-g1-check
  lastBuildJobName: demo-app-policy-b-abc123-g1-build
  lastBuiltImage: localhost:5001/alt/demo-app:demo-app-policy-gen1-20260501100015
  lastAppliedImage: localhost:5001/alt/demo-app:demo-app-policy-gen1-20260501100015
  targetDeploymentGeneration: 4
  reason: RolloutCompleted
  message: Deployment demo-app successfully rolled out with image localhost:5001/alt/demo-app:demo-app-policy-gen1-20260501100015
  conditions:
    - type: Ready
      status: "True"
      observedGeneration: 1
      lastTransitionTime: "2026-05-01T10:02:55Z"
      reason: Succeeded
      message: Policy completed successfully.
    - type: BuildCompleted
      status: "True"
      observedGeneration: 1
      lastTransitionTime: "2026-05-01T10:02:10Z"
      reason: JobCompleted
      message: Build Job completed successfully.
    - type: RolloutCompleted
      status: "True"
      observedGeneration: 1
      lastTransitionTime: "2026-05-01T10:02:55Z"
      reason: DeploymentAvailable
      message: Deployment rollout completed.
```

## 7.5. Status fields reference

| Поле                         |         Тип | Описание                                                |
| ---------------------------- | ----------: | ------------------------------------------------------- |
| `observedGeneration`         |       int64 | Последняя обработанная `metadata.generation`.           |
| `phase`                      |      string | Текущая фаза state machine.                             |
| `buildID`                    |      string | Стабильный id текущего запуска.                         |
| `currentRunKey`              |      string | Внутренний ключ запуска: generation + hash manualToken. |
| `lastCheckTime`              | metav1.Time | Время завершения или последней обработки check stage.   |
| `lastBuildStartTime`         | metav1.Time | Время начала build stage.                               |
| `lastBuildCompletionTime`    | metav1.Time | Время завершения build stage.                           |
| `lastApplyTime`              | metav1.Time | Время patch Deployment.                                 |
| `lastRolloutTime`            | metav1.Time | Время успешного rollout.                                |
| `lastCheckJobName`           |      string | Имя последней Check Job.                                |
| `lastBuildJobName`           |      string | Имя последней Build Job.                                |
| `lastBuiltImage`             |      string | Последний собранный image reference.                    |
| `lastAppliedImage`           |      string | Последний примененный image reference.                  |
| `targetDeploymentGeneration` |       int64 | Generation Deployment после patch.                      |
| `reason`                     |      string | Машиночитаемая причина текущего состояния.              |
| `message`                    |      string | Человекочитаемое сообщение.                             |
| `conditions`                 | []Condition | Conditions по стандартному Kubernetes-формату.          |

## 7.6. Conditions

### `Ready`

Общее состояние policy.

* `True`: policy завершилась успешно или находится в корректном no-op состоянии `UpToDate`.
* `False`: policy выполняется или завершилась ошибкой.
* `Unknown`: начальное состояние или неопределенность.

### `CheckCompleted`

Check stage завершился.

### `UpdatesAvailable`

Результат `AltAptSimulation`.

* `True`: apt simulation показал доступные изменения.
* `False`: изменений нет.
* `Unknown`: check не выполнен или завершился ошибкой.

### `BuildCompleted`

Build Job завершилась успешно.

### `ImagePublished`

Итоговый image опубликован в registry.

В MVP эта condition устанавливается в `True`, если Build Job Kaniko завершилась успешно. Дополнительная проверка pull image может быть follow-up.

### `DeploymentUpdated`

Deployment успешно пропатчен.

### `RolloutCompleted`

Rollout целевого Deployment завершился успешно.

### `UpToDate`

Для `AltAptSimulation`, когда обновлений нет.

### `Failed`

Терминальная ошибка для текущего run.

---

# 8. State machine / reconcile algorithm

## 8.1. Фазы

```text
Pending
  -> Checking
  -> UpToDate
  -> Building
  -> Publishing
  -> Applying
  -> RollingOut
  -> Succeeded

Any phase
  -> Failed
```

## 8.2. Run identity

Для каждого reconcile controller вычисляет `runKey`:

```text
runKey = metadata.generation + ":" + sha256(spec.trigger.manualToken)
```

И `buildID`:

```text
buildID = "b-" + short(policy.UID) + "-g" + metadata.generation + optional("-" + shortHash(manualToken))
```

Правила:

* если `status.currentRunKey != runKey`, начинается новый run;
* если `status.currentRunKey == runKey` и phase `Succeeded`, новый run не начинается;
* если `status.currentRunKey == runKey` и phase `Failed`, новый run не начинается до изменения spec/manualToken;
* если Job уже существует для `buildID`, новая Job не создается.

## 8.3. High-level reconcile algorithm

Псевдокод:

```text
Reconcile(policy):

1. Load AltImageUpdatePolicy.
2. If deleted: return.
3. Apply defaults in memory.
4. Validate unsupported MVP combinations.
5. Compute runKey and buildID.
6. If new run:
     initialize status:
       observedGeneration = metadata.generation
       currentRunKey = runKey
       buildID = buildID
       phase = Pending
       Ready=False
7. Validate target Deployment exists.
8. Validate target container exists.
9. Validate ConfigMap context exists.
10. Validate Dockerfile key exists.
11. If check.mode == AltAptSimulation:
      ensure Check Job exists.
      if Check Job active:
          phase = Checking
          requeue after 5s
      if Check Job failed:
          phase = Failed
          Failed=True reason=CheckFailed
          return
      if Check Job complete:
          read logs
          parse logs
          if no updates:
              phase = UpToDate
              Ready=True
              UpToDate=True
              return
          if updates:
              UpdatesAvailable=True
              continue to build
12. If check.mode == Always:
      skip Check Job and continue to build.
13. Ensure Build Job exists.
14. If Build Job active:
      phase = Building
      requeue after 5s
15. If Build Job failed:
      phase = Failed
      Failed=True reason=BuildFailed
      return
16. If Build Job complete:
      phase = Applying
      lastBuiltImage = generated image
17. If Deployment already has target image and annotation for buildID:
      skip patch
   Else:
      patch target Deployment container image
      set annotations
      phase = RollingOut
18. Observe rollout:
      if rollout complete:
          phase = Succeeded
          Ready=True
          RolloutCompleted=True
          return
      if rollout timeout exceeded:
          phase = Failed
          Failed=True reason=RolloutTimeout
          return
      else:
          requeue after 5s
```

## 8.4. Идемпотентность

Оператор должен использовать следующие механизмы идемпотентности:

1. Детерминированный `buildID`.
2. Детерминированные имена Jobs.
3. Labels на Jobs.
4. Проверка существования Job перед созданием.
5. Проверка текущего image в Deployment перед patch.
6. Проверка аннотации `security.altlinux.org/last-build-id`.
7. Сравнение `status.currentRunKey`.
8. Стабильный generated image tag для одного run.

## 8.5. Requeue

| Состояние           |             Requeue |
| ------------------- | ------------------: |
| Check Job active    |  `RequeueAfter: 5s` |
| Build Job active    |  `RequeueAfter: 5s` |
| Rollout pending     |  `RequeueAfter: 5s` |
| Terminal success    |          no requeue |
| Terminal failure    | no explicit requeue |
| API conflict        |       default retry |
| transient API error |       default retry |

## 8.6. Failure handling

Терминальные ошибки:

```text
UnsupportedBuilder
UnsupportedTargetKind
BuildContextNotFound
DockerfileKeyNotFound
TargetDeploymentNotFound
TargetContainerNotFound
CheckJobFailed
CheckLogReadFailed
BuildJobFailed
DeploymentPatchFailed
RolloutTimeout
RolloutFailed
```

При терминальной ошибке controller должен:

* установить `status.phase: Failed`;
* установить `Ready=False`;
* установить `Failed=True`;
* записать `reason`;
* записать `message`;
* создать Kubernetes Event `PolicyFailed`;
* не запускать новый pipeline до изменения `generation` или `manualToken`.

---

# 9. Check Job design

## 9.1. Назначение

Check Job используется для определения, есть ли доступные обновления в ALT Linux base environment.

Поддерживается только для:

```yaml
spec:
  check:
    mode: AltAptSimulation
```

## 9.2. Job image

Check Job использует:

```yaml
spec:
  alt:
    baseImage: alt:p10
```

То есть контейнер Check Job запускается из `spec.alt.baseImage`.

## 9.3. Команда Check Job

Команда:

```bash
set -euo pipefail
apt-get update
apt-get -s dist-upgrade
```

Если `/bin/bash` отсутствует, используется `/bin/sh`:

```bash
set -eu
apt-get update
apt-get -s dist-upgrade
```

MVP-рекомендация: использовать `/bin/sh`.

Полная command/args:

```yaml
command:
  - /bin/sh
  - -c
args:
  - |
    set -eu
    echo "[alt-image-update] branch=${ALT_BRANCH}"
    echo "[alt-image-update] running apt-get update"
    apt-get update
    echo "[alt-image-update] running apt-get -s dist-upgrade"
    apt-get -s dist-upgrade
```

## 9.4. Environment variables

```yaml
env:
  - name: ALT_BRANCH
    value: p10
  - name: ALT_BASE_IMAGE
    value: alt:p10
  - name: POLICY_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.labels['security.altlinux.org/policy-name']
```

## 9.5. Labels

```yaml
labels:
  app.kubernetes.io/name: alt-image-update-operator
  app.kubernetes.io/managed-by: alt-image-update-operator
  security.altlinux.org/policy-name: demo-app-policy
  security.altlinux.org/policy-uid: "<uid>"
  security.altlinux.org/build-id: "<buildID>"
  security.altlinux.org/job-type: check
```

## 9.6. Job spec

Default:

```yaml
backoffLimit: 0
ttlSecondsAfterFinished: 600
activeDeadlineSeconds: 900
```

## 9.7. Log collection

Controller должен:

1. найти pod, созданный Check Job;
2. дождаться завершения Job;
3. прочитать logs pod'а через Kubernetes API `pods/log`;
4. передать logs в parser;
5. обновить status.

Если pod не найден после завершения Job, controller должен выставить:

```text
phase: Failed
reason: CheckPodNotFound
```

Если logs не удалось прочитать:

```text
phase: Failed
reason: CheckLogReadFailed
```

## 9.8. Parser result

Parser должен возвращать:

```go
type CheckResult string

const (
    CheckResultUpdatesAvailable CheckResult = "UpdatesAvailable"
    CheckResultNoUpdates         CheckResult = "NoUpdates"
    CheckResultUnknown           CheckResult = "Unknown"
)
```

Вход:

```go
ParseAltAptSimulationLog(log string) CheckResult
```

Минимальная логика:

```text
if contains "Inst " -> UpdatesAvailable
else if contains "The following packages will be upgraded" -> UpdatesAvailable
else if contains "The following NEW packages will be installed" -> UpdatesAvailable
else if contains "The following packages will be REMOVED" -> UpdatesAvailable
else -> NoUpdates
```

## 9.9. Check Job acceptance

Check Job считается успешной, если:

* Kubernetes Job condition `Complete=True`;
* exit code контейнера `0`;
* logs доступны;
* parser вернул `UpdatesAvailable` или `NoUpdates`.

---

# 10. Build Job design

## 10.1. Назначение

Build Job выполняет реальную сборку контейнерного образа на основе Dockerfile из ConfigMap и публикует образ в registry.

## 10.2. Builder

MVP builder:

```text
Kaniko
```

Default builder image:

```text
gcr.io/kaniko-project/executor:latest
```

Поле переопределения:

```yaml
spec:
  build:
    builderImage: gcr.io/kaniko-project/executor:latest
```

## 10.3. Kaniko command

Kaniko запускается как container command:

```yaml
args:
  - "--context=dir:///workspace"
  - "--dockerfile=/workspace/Dockerfile"
  - "--destination=localhost:5001/alt/demo-app:demo-app-policy-gen1-20260501100015"
  - "--digest-file=/tekton/results/image-digest"
```

Так как `/tekton/results` может отсутствовать, MVP должен использовать путь, созданный через `emptyDir`:

```yaml
volumeMounts:
  - name: kaniko-results
    mountPath: /results
args:
  - "--digest-file=/results/image-digest"
```

Если digest-file не используется в status, аргумент можно не передавать. Но для расширяемости рекомендуется использовать.

## 10.4. Build context mount

ConfigMap монтируется:

```yaml
volumes:
  - name: build-context
    configMap:
      name: demo-app-context
volumeMounts:
  - name: build-context
    mountPath: /workspace
    readOnly: true
```

Dockerfile path:

```text
/workspace/<spec.build.context.configMapRef.dockerfileKey>
```

## 10.5. Registry auth

Если задано:

```yaml
registrySecretRef:
  name: registry-auth
```

Secret должен быть смонтирован:

```yaml
volumes:
  - name: kaniko-docker-config
    secret:
      secretName: registry-auth
volumeMounts:
  - name: kaniko-docker-config
    mountPath: /kaniko/.docker
    readOnly: true
```

Ожидаемый формат Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: registry-auth
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: <base64>
```

Для Kaniko может потребоваться key path:

```text
/kaniko/.docker/config.json
```

Если используется Secret типа `kubernetes.io/dockerconfigjson`, реализация должна монтировать `.dockerconfigjson` как `config.json` через `items`:

```yaml
items:
  - key: .dockerconfigjson
    path: config.json
```

## 10.6. Image tag generation

Input:

```yaml
outputImage: localhost:5001/alt/demo-app
tagTemplate: "{{ .Name }}-gen{{ .Generation }}-{{ .Timestamp }}"
```

Generated:

```text
localhost:5001/alt/demo-app:demo-app-policy-gen1-20260501100015
```

Template context:

```go
type TagTemplateContext struct {
    Name               string
    Namespace          string
    Generation         int64
    ObservedGeneration int64
    ManualTokenHash    string
    Timestamp          string
    ShortUID           string
}
```

Tag sanitization:

* lowercase is recommended but not strictly required by Docker tags;
* replace invalid chars with `-`;
* trim to 128 characters;
* remove leading `.` or `-`;
* if empty after sanitization, use `build-<Timestamp>`.

## 10.7. Job labels

```yaml
labels:
  app.kubernetes.io/name: alt-image-update-operator
  app.kubernetes.io/managed-by: alt-image-update-operator
  security.altlinux.org/policy-name: demo-app-policy
  security.altlinux.org/policy-uid: "<uid>"
  security.altlinux.org/build-id: "<buildID>"
  security.altlinux.org/job-type: build
```

## 10.8. Build Job spec

Default:

```yaml
backoffLimit: 0
ttlSecondsAfterFinished: 600
activeDeadlineSeconds: 900
```

Container resources default:

```yaml
resources:
  requests:
    cpu: 250m
    memory: 512Mi
  limits:
    cpu: "2"
    memory: 2Gi
```

Assumption: resource fields may be added to CRD later. For MVP hardcoded conservative defaults are acceptable.

## 10.9. Build Job success

Build Job считается успешной, если:

* Kubernetes Job condition `Complete=True`;
* container exit code `0`;
* generated image reference известен controller'у;
* Kaniko не завершился ошибкой.

После success controller должен:

```yaml
status:
  phase: Applying
  lastBuiltImage: <generated-image>
  lastBuildCompletionTime: <now>
conditions:
  - type: BuildCompleted
    status: "True"
  - type: ImagePublished
    status: "True"
```

## 10.10. Build Job failure

Build Job считается неуспешной, если:

* Kubernetes Job condition `Failed=True`;
* activeDeadlineSeconds exceeded;
* pod failed;
* Kaniko exit code non-zero.

После failure controller должен:

```yaml
status:
  phase: Failed
  reason: BuildJobFailed
conditions:
  - type: Ready
    status: "False"
  - type: Failed
    status: "True"
  - type: BuildCompleted
    status: "False"
```

## 10.11. BuildKit follow-up

CRD может быть расширен в будущем:

```yaml
spec:
  build:
    builder: BuildKit
```

В MVP BuildKit не реализуется.

Если поле `BuildKit` разрешено схемой, controller должен выставить:

```text
phase: Failed
reason: UnsupportedBuilder
```

Рекомендуемое MVP-решение: не разрешать `BuildKit` в OpenAPI enum до реализации.

---

# 11. Deployment update design

## 11.1. Target

MVP поддерживает только:

```yaml
targetRef:
  apiVersion: apps/v1
  kind: Deployment
  name: demo-app
```

Target namespace всегда равен namespace `AltImageUpdatePolicy`.

## 11.2. Preflight validation

Перед Check/Build controller должен проверить:

1. Deployment существует;
2. Deployment имеет контейнер с `spec.containerName`;
3. Deployment не находится в состоянии удаления.

Если preflight validation неуспешна, pipeline не запускается.

## 11.3. Patch strategy

Controller должен использовать Kubernetes API patch/update.

Рекомендуемый подход:

1. получить Deployment;
2. создать deep copy;
3. изменить `spec.template.spec.containers[i].image`;
4. установить annotations;
5. выполнить `client.Patch(ctx, modified, client.MergeFrom(original))`.

## 11.4. Container update

Controller должен найти контейнер:

```go
for i := range deployment.Spec.Template.Spec.Containers {
    if deployment.Spec.Template.Spec.Containers[i].Name == policy.Spec.ContainerName {
        deployment.Spec.Template.Spec.Containers[i].Image = generatedImage
    }
}
```

Если контейнер не найден:

```text
phase: Failed
reason: TargetContainerNotFound
```

## 11.5. Annotations

Controller должен установить:

```yaml
spec:
  template:
    metadata:
      annotations:
        security.altlinux.org/last-build-id: "<buildID>"
        security.altlinux.org/last-built-image: "<generatedImage>"
        security.altlinux.org/last-applied-at: "<RFC3339 timestamp>"
        security.altlinux.org/policy-name: "<policy.Name>"
        security.altlinux.org/policy-namespace: "<policy.Namespace>"
```

## 11.6. Idempotent patch

Если Deployment уже содержит:

```text
container image == generatedImage
security.altlinux.org/last-build-id == buildID
```

controller не должен выполнять patch повторно.

В этом случае он должен перейти к rollout observation.

## 11.7. Rollout start time

После patch controller должен записать:

```yaml
status:
  phase: RollingOut
  lastApplyTime: <now>
  targetDeploymentGeneration: <deployment.metadata.generation after patch>
```

## 11.8. Rollout observation

Controller должен считать rollout успешным, если:

```text
deployment.status.observedGeneration >= deployment.metadata.generation
deployment.status.updatedReplicas == deployment.spec.replicas
deployment.status.readyReplicas == deployment.spec.replicas
deployment.status.unavailableReplicas == 0 or nil
Available condition == True
Progressing condition == True
```

Если `spec.replicas` пустой, считать expected replicas равным `1`.

## 11.9. Rollout timeout calculation

Timeout начинается от:

```text
status.lastApplyTime
```

Если `lastApplyTime` пустой, timeout начинается от текущего времени при первом входе в `RollingOut`.

Default timeout:

```text
300 seconds
```

## 11.10. Rollout failure

Если timeout истек:

```yaml
status:
  phase: Failed
  reason: RolloutTimeout
  message: Deployment demo-app did not become ready within 300 seconds.
conditions:
  - type: RolloutCompleted
    status: "False"
    reason: RolloutTimeout
  - type: Failed
    status: "True"
```

## 11.11. Rollout success

При успехе:

```yaml
status:
  phase: Succeeded
  lastRolloutTime: <now>
  lastAppliedImage: <generatedImage>
  reason: RolloutCompleted
  message: Deployment demo-app successfully rolled out.
conditions:
  - type: Ready
    status: "True"
    reason: Succeeded
  - type: DeploymentUpdated
    status: "True"
  - type: RolloutCompleted
    status: "True"
  - type: Failed
    status: "False"
```

---

# 12. RBAC и безопасность

## 12.1. Controller permissions

Controller должен иметь права на `AltImageUpdatePolicy`:

```yaml
resources:
  - altimageupdatepolicies
  - altimageupdatepolicies/status
  - altimageupdatepolicies/finalizers
verbs:
  - get
  - list
  - watch
  - create
  - update
  - patch
  - delete
```

Для status:

```yaml
resources:
  - altimageupdatepolicies/status
verbs:
  - get
  - update
  - patch
```

## 12.2. Jobs permissions

```yaml
apiGroups:
  - batch
resources:
  - jobs
verbs:
  - get
  - list
  - watch
  - create
  - update
  - patch
  - delete
```

## 12.3. Pods permissions

```yaml
apiGroups:
  - ""
resources:
  - pods
verbs:
  - get
  - list
  - watch
```

## 12.4. Pod logs permissions

```yaml
apiGroups:
  - ""
resources:
  - pods/log
verbs:
  - get
```

## 12.5. Deployment permissions

```yaml
apiGroups:
  - apps
resources:
  - deployments
verbs:
  - get
  - list
  - watch
  - update
  - patch
```

## 12.6. ConfigMap permissions

```yaml
apiGroups:
  - ""
resources:
  - configmaps
verbs:
  - get
  - list
  - watch
```

## 12.7. Secret permissions

```yaml
apiGroups:
  - ""
resources:
  - secrets
verbs:
  - get
```

Secret read нужен только для проверки существования `registrySecretRef`. Controller не должен читать или логировать содержимое Secret.

## 12.8. Events permissions

```yaml
apiGroups:
  - ""
resources:
  - events
verbs:
  - create
  - patch
```

Для новых Kubernetes versions также может потребоваться:

```yaml
apiGroups:
  - events.k8s.io
resources:
  - events
verbs:
  - create
  - patch
```

## 12.9. SecurityContext для Jobs

MVP default для Job containers:

```yaml
securityContext:
  allowPrivilegeEscalation: false
  runAsNonRoot: false
```

Assumption: Kaniko может требовать root внутри контейнера для сборки некоторых образов. Поэтому `runAsNonRoot: false` допустим для MVP.

Запрещено:

* privileged containers;
* Docker socket mount;
* hostPath Docker socket;
* запуск Docker daemon внутри pod.

## 12.10. Network

MVP требует сетевой доступ Job pod'ов к:

* ALT Linux package repositories;
* container registry.

Оператор не управляет NetworkPolicy в MVP.

## 12.11. Secret handling

Запрещено записывать в status:

* registry username;
* registry password;
* Docker config content;
* token values.

`manualToken` может быть записан только в виде hash, если требуется.

## 12.12. Supply-chain security limitations

MVP не выполняет:

* image signing;
* SBOM;
* vulnerability scan;
* provenance attestation.

Эти функции относятся к follow-up.

---

# 13. Observability

## 13.1. Kubernetes Events

Оператор должен создавать события на объекте `AltImageUpdatePolicy`.

Минимальный набор:

| Event reason              | Type    | Когда создается                         |
| ------------------------- | ------- | --------------------------------------- |
| `CheckStarted`            | Normal  | Создана Check Job.                      |
| `CheckCompleted`          | Normal  | Check Job завершилась успешно.          |
| `CheckFailed`             | Warning | Check Job завершилась ошибкой.          |
| `BuildStarted`            | Normal  | Создана Build Job.                      |
| `BuildCompleted`          | Normal  | Build Job завершилась успешно.          |
| `BuildFailed`             | Warning | Build Job завершилась ошибкой.          |
| `DeploymentUpdateStarted` | Normal  | Начат patch Deployment.                 |
| `DeploymentUpdated`       | Normal  | Deployment успешно обновлен.            |
| `RolloutCompleted`        | Normal  | Deployment rollout успешен.             |
| `RolloutFailed`           | Warning | Rollout завершился ошибкой или timeout. |
| `PolicySucceeded`         | Normal  | Весь pipeline завершился успешно.       |
| `PolicyFailed`            | Warning | Pipeline завершился ошибкой.            |

## 13.2. Controller logs

Logs должны быть структурированными через controller-runtime logger.

Пример:

```text
INFO Reconcile started namespace=alt-update-demo name=demo-app-policy generation=1
INFO Build job created namespace=alt-update-demo name=demo-app-policy buildID=b-abc123-g1 image=localhost:5001/alt/demo-app:demo-app-policy-gen1-20260501100015
INFO Deployment patched namespace=alt-update-demo deployment=demo-app container=app image=localhost:5001/alt/demo-app:demo-app-policy-gen1-20260501100015
ERROR Build job failed namespace=alt-update-demo name=demo-app-policy reason=BuildJobFailed
```

## 13.3. Metrics

Controller-runtime default metrics должны быть включены.

Кастомные метрики:

### `alt_image_update_reconcile_total`

Counter.

Labels:

```text
namespace
policy
result
reason
```

### `alt_image_update_check_total`

Counter.

Labels:

```text
namespace
policy
result
reason
```

Results:

```text
started
updates_available
no_updates
failed
```

### `alt_image_update_build_total`

Counter.

Labels:

```text
namespace
policy
result
reason
```

Results:

```text
started
succeeded
failed
```

### `alt_image_update_deployment_update_total`

Counter.

Labels:

```text
namespace
policy
result
reason
```

Results:

```text
patched
skipped
failed
```

### `alt_image_update_rollout_total`

Counter.

Labels:

```text
namespace
policy
result
reason
```

Results:

```text
succeeded
timeout
failed
```

### `alt_image_update_failure_total`

Counter.

Labels:

```text
namespace
policy
reason
```

## 13.4. Status as observability surface

`status.conditions` является основным пользовательским интерфейсом состояния.

Команда проверки:

```bash
kubectl get altimageupdatepolicy demo-app-policy -n alt-update-demo -o yaml
```

Краткий вывод:

```bash
kubectl get aiup -n alt-update-demo
```

CRD additional printer columns должны включать:

```text
PHASE
READY
LAST-BUILT-IMAGE
LAST-APPLIED-IMAGE
AGE
```

Пример Kubebuilder annotations:

```go
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="LastBuiltImage",type=string,JSONPath=`.status.lastBuiltImage`
// +kubebuilder:printcolumn:name="LastAppliedImage",type=string,JSONPath=`.status.lastAppliedImage`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
```

---

# 14. Тестовая стратегия

## 14.1. Unit tests

Unit tests должны покрывать pure logic без Kubernetes API.

Обязательные тестируемые компоненты:

### Check log parser

Файл:

```text
internal/check/parser_test.go
```

Тесты:

1. log содержит `Inst package` -> `UpdatesAvailable`;
2. log содержит `The following packages will be upgraded` -> `UpdatesAvailable`;
3. log содержит `The following NEW packages will be installed` -> `UpdatesAvailable`;
4. log содержит `The following packages will be REMOVED` -> `UpdatesAvailable`;
5. log успешный без паттернов -> `NoUpdates`;
6. пустой log при успешной Job -> `NoUpdates`.

### Image tag generator

Файл:

```text
internal/image/tag_test.go
```

Тесты:

1. default template создает tag;
2. одинаковый run создает одинаковый tag;
3. invalid chars заменяются;
4. tag не превышает 128 символов;
5. outputImage без tag получает tag;
6. outputImage с tag заменяет tag.

### Build ID generator

Файл:

```text
internal/run/build_id_test.go
```

Тесты:

1. same generation/manualToken -> same buildID;
2. changed generation -> different buildID;
3. changed manualToken -> different buildID;
4. buildID соответствует DNS-1123 suffix constraints.

### Deployment patch helper

Файл:

```text
internal/deploy/patch_test.go
```

Тесты:

1. patch меняет только нужный container;
2. patch не меняет sidecar;
3. отсутствующий container возвращает error;
4. annotations добавляются;
5. повторный patch распознается как no-op.

### Condition helper

Файл:

```text
internal/status/conditions_test.go
```

Тесты:

1. condition создается;
2. condition обновляется без лишней смены `lastTransitionTime`, если status/reason/message не изменились;
3. observedGeneration проставляется.

## 14.2. Envtest tests

Envtest должен проверять Kubernetes API behavior без реального kind.

Файл:

```text
internal/controller/altimageupdatepolicy_controller_envtest_test.go
```

Обязательные сценарии:

### ET-001. CRD validation

* CR без `spec.build.outputImage` отклоняется.
* CR с `spec.alt.branch: invalid` отклоняется.
* CR с `spec.check.mode: Unknown` отклоняется.

### ET-002. Preflight target missing

* Создать policy без Deployment.
* Reconcile.
* Проверить `status.phase=Failed`, reason `TargetDeploymentNotFound`.

### ET-003. Container missing

* Создать Deployment без нужного container.
* Создать policy.
* Reconcile.
* Проверить `TargetContainerNotFound`.

### ET-004. ConfigMap missing

* Создать Deployment.
* Не создавать ConfigMap.
* Reconcile.
* Проверить `BuildContextNotFound`.

### ET-005. Dockerfile key missing

* Создать ConfigMap без ключа `Dockerfile`.
* Reconcile.
* Проверить `DockerfileKeyNotFound`.

### ET-006. Always creates Build Job

* Создать Deployment, ConfigMap, policy `Always`.
* Reconcile.
* Проверить созданную Build Job с правильными args.

### ET-007. Idempotency

* Повторить reconcile.
* Проверить, что Build Job одна.

### ET-008. Deployment patch after fake completed Job

В envtest допустимо вручную создать Job status `Complete=True` или использовать fake status update для проверки controller transition.

Проверить:

* Deployment image изменен;
* annotations установлены;
* status phase `RollingOut` или `Succeeded`, если Deployment status готов.

## 14.3. E2E tests в kind/minikube

E2E должен использовать реальный Kubernetes-контур.

Обязательные компоненты:

* CRD установлен в cluster;
* controller-manager запущен в cluster;
* local registry доступен;
* demo ConfigMap содержит Dockerfile;
* demo Deployment создан;
* `AltImageUpdatePolicy` создан;
* Kaniko Build Job реально собирает image;
* image реально публикуется в registry;
* Deployment реально обновляется;
* Pod реально стартует с новым image;
* status policy становится `Succeeded`.

## 14.4. E2E: kind local registry

Рекомендуемый сценарий:

1. Запустить local registry container:

```bash
docker run -d --restart=always -p 5001:5000 --name kind-registry registry:2
```

2. Создать kind cluster с containerd registry mirror для `localhost:5001`.

3. Подключить registry container к kind network:

```bash
docker network connect kind kind-registry || true
```

4. Установить ConfigMap local registry hosting, если используется kind convention.

5. Использовать image repository:

```text
localhost:5001/alt/demo-app
```

Assumption: конкретный kind registry setup документируется в `docs/demo.md`.

## 14.5. E2E test cases

### E2E-001. Always mode full pipeline

Шаги:

1. Создать namespace `alt-update-demo`.
2. Установить operator.
3. Создать demo Deployment с initial image.
4. Создать ConfigMap с Dockerfile.
5. Создать `AltImageUpdatePolicy` с `check.mode: Always`.
6. Дождаться Build Job `Complete=True`.
7. Дождаться Deployment rollout.
8. Проверить status CR.

Ожидаемый результат:

```yaml
status:
  phase: Succeeded
  conditions:
    - type: Ready
      status: "True"
```

### E2E-002. Manual retrigger

Шаги:

1. Взять успешный policy.
2. Изменить `spec.trigger.manualToken`.
3. Дождаться новой Build Job.
4. Проверить, что Deployment image tag изменился.

Ожидаемый результат:

* Jobs count увеличился на 1 build job;
* `status.lastBuiltImage` изменился;
* `status.phase=Succeeded`.

### E2E-003. Idempotent reconcile

Шаги:

1. После successful run подождать 30 секунд.
2. Проверить количество Build Jobs.
3. Не менять CR.
4. Подождать еще 30 секунд.
5. Проверить количество Build Jobs.

Ожидаемый результат:

* количество Build Jobs не изменилось;
* Deployment generation не увеличивается бесконечно.

### E2E-004. Target container missing

Шаги:

1. Создать Deployment без container `app`.
2. Создать policy с `containerName: app`.
3. Проверить status.

Ожидаемый результат:

```yaml
status:
  phase: Failed
  reason: TargetContainerNotFound
```

### E2E-005. AltAptSimulation mode

Шаги:

1. Создать policy с `check.mode: AltAptSimulation`.
2. Дождаться Check Job.
3. Проверить logs Check Job.
4. Проверить дальнейшее поведение.

Ожидаемый результат:

* Check Job реально выполняет `apt-get update`;
* Check Job реально выполняет `apt-get -s dist-upgrade`;
* если parser видит updates, Build Job запускается;
* если updates нет, status `UpToDate`.

## 14.6. Запрет fake в e2e

В e2e запрещено:

* подменять Kaniko fake container;
* использовать Job, который только печатает success;
* вручную выставлять Job status;
* вручную патчить Deployment вместо controller;
* вручную записывать status policy.

---

# 15. Demo-сценарий для защиты ВКР

## 15.1. Цель demo

Показать, что оператор реально автоматизирует процесс:

```text
Policy -> Build Job -> Kaniko build -> registry push -> Deployment image update -> rollout -> status
```

## 15.2. Предварительные требования

На машине demo:

```text
docker
kubectl
kind или minikube
make
go
```

Для kind demo:

```text
local registry on localhost:5001
kind cluster with registry access
```

## 15.3. Команды demo

### 15.3.1. Создать cluster и registry

```bash
make kind-create
```

Ожидаемый результат:

```bash
kubectl cluster-info
```

показывает доступный cluster.

Registry доступен:

```bash
curl http://localhost:5001/v2/_catalog
```

### 15.3.2. Установить CRD

```bash
make install
```

Проверка:

```bash
kubectl get crd altimageupdatepolicies.security.altlinux.org
```

### 15.3.3. Собрать и загрузить operator image

```bash
make docker-build IMG=localhost:5001/alt/alt-image-update-operator:demo
make docker-push IMG=localhost:5001/alt/alt-image-update-operator:demo
```

### 15.3.4. Запустить operator в cluster

```bash
make deploy IMG=localhost:5001/alt/alt-image-update-operator:demo
```

Проверка:

```bash
kubectl -n alt-image-update-operator-system get pods
```

Ожидаемый результат:

```text
controller-manager pod Running
```

### 15.3.5. Создать demo namespace

```bash
kubectl create namespace alt-update-demo
```

### 15.3.6. Создать initial Deployment

```bash
kubectl apply -n alt-update-demo -f config/demo/deployment.yaml
```

Проверка:

```bash
kubectl rollout status deployment/demo-app -n alt-update-demo
kubectl get deployment demo-app -n alt-update-demo -o jsonpath='{.spec.template.spec.containers[?(@.name=="app")].image}'
```

Ожидаемый initial image:

```text
busybox:1.36
```

или другой заранее определенный initial image, отличный от итогового Kaniko image.

### 15.3.7. Создать ConfigMap build context

```bash
kubectl apply -n alt-update-demo -f config/demo/context-configmap.yaml
```

Проверка:

```bash
kubectl get configmap demo-app-context -n alt-update-demo -o yaml
```

В ConfigMap должен быть key:

```text
Dockerfile
```

### 15.3.8. Создать AltImageUpdatePolicy

```bash
kubectl apply -n alt-update-demo -f config/demo/policy-always.yaml
```

Проверка:

```bash
kubectl get aiup -n alt-update-demo
```

Ожидаемый результат:

```text
NAME              PHASE      READY
demo-app-policy   Building   False
```

или другая промежуточная фаза.

### 15.3.9. Наблюдать Build Job

```bash
kubectl get jobs -n alt-update-demo -w
```

Ожидаемый результат:

```text
demo-app-policy-...-build   Complete
```

Проверка логов Kaniko:

```bash
BUILD_POD="$(kubectl get pods -n alt-update-demo -l security.altlinux.org/job-type=build -o jsonpath='{.items[0].metadata.name}')"
kubectl logs -n alt-update-demo "$BUILD_POD"
```

В логах должны быть признаки реальной сборки:

```text
apt-get update
dist-upgrade
Pushing image to localhost:5001/alt/demo-app
```

### 15.3.10. Проверить registry

```bash
curl http://localhost:5001/v2/_catalog
```

Ожидаемый результат содержит:

```text
alt/demo-app
```

Проверить tags:

```bash
curl http://localhost:5001/v2/alt/demo-app/tags/list
```

Ожидаемый результат содержит generated tag.

### 15.3.11. Проверить Deployment update

```bash
kubectl get deployment demo-app -n alt-update-demo -o jsonpath='{.spec.template.spec.containers[?(@.name=="app")].image}'
```

Ожидаемый image:

```text
localhost:5001/alt/demo-app:<generated-tag>
```

Проверить annotations:

```bash
kubectl get deployment demo-app -n alt-update-demo -o jsonpath='{.spec.template.metadata.annotations.security\.altlinux\.org/last-build-id}'
```

Ожидаемый результат:

```text
b-...
```

### 15.3.12. Проверить rollout

```bash
kubectl rollout status deployment/demo-app -n alt-update-demo
```

Ожидаемый результат:

```text
deployment "demo-app" successfully rolled out
```

### 15.3.13. Проверить status CR

```bash
kubectl get aiup demo-app-policy -n alt-update-demo -o yaml
```

Ожидаемые поля:

```yaml
status:
  phase: Succeeded
  lastBuildJobName: ...
  lastBuiltImage: localhost:5001/alt/demo-app:...
  lastAppliedImage: localhost:5001/alt/demo-app:...
  conditions:
    - type: Ready
      status: "True"
    - type: BuildCompleted
      status: "True"
    - type: DeploymentUpdated
      status: "True"
    - type: RolloutCompleted
      status: "True"
```

### 15.3.14. Проверить manual retrigger

```bash
kubectl patch aiup demo-app-policy -n alt-update-demo --type merge -p \
  '{"spec":{"trigger":{"manualToken":"demo-run-002"}}}'
```

Проверить новую сборку:

```bash
kubectl get jobs -n alt-update-demo
kubectl get aiup demo-app-policy -n alt-update-demo -o jsonpath='{.status.lastBuiltImage}'
```

Ожидаемый результат:

* создана новая Build Job;
* tag изменился;
* Deployment снова успешно rolled out.

## 15.4. Как доказать корректность на защите

Показать последовательно:

1. CRD существует:

```bash
kubectl get crd altimageupdatepolicies.security.altlinux.org
```

2. CR описывает policy:

```bash
kubectl get aiup demo-app-policy -n alt-update-demo -o yaml
```

3. Build Job реально выполнялся:

```bash
kubectl get jobs -n alt-update-demo
kubectl logs -n alt-update-demo "$BUILD_POD"
```

4. В логах Build Job есть `apt-get update` и `dist-upgrade`.

5. Registry содержит новый image:

```bash
curl http://localhost:5001/v2/alt/demo-app/tags/list
```

6. Deployment image изменился:

```bash
kubectl get deploy demo-app -n alt-update-demo -o jsonpath='{.spec.template.spec.containers[?(@.name=="app")].image}'
```

7. Rollout успешен:

```bash
kubectl rollout status deployment/demo-app -n alt-update-demo
```

8. Status CR отражает результат:

```bash
kubectl get aiup demo-app-policy -n alt-update-demo -o jsonpath='{.status.phase}'
```

Ожидаемый вывод:

```text
Succeeded
```

---

# 16. Repository layout

Рекомендуемая структура репозитория:

```text
alt-image-update-operator/
  PRD.md
  README.md
  Makefile
  go.mod
  go.sum

  api/
    v1alpha1/
      altimageupdatepolicy_types.go
      groupversion_info.go
      zz_generated.deepcopy.go

  cmd/
    main.go

  internal/
    controller/
      altimageupdatepolicy_controller.go
      altimageupdatepolicy_controller_test.go
      altimageupdatepolicy_controller_envtest_test.go

    check/
      parser.go
      parser_test.go

    deploy/
      patch.go
      patch_test.go
      rollout.go
      rollout_test.go

    image/
      reference.go
      tag.go
      tag_test.go

    jobs/
      check_job.go
      build_job.go
      names.go
      names_test.go

    metrics/
      metrics.go

    run/
      build_id.go
      build_id_test.go

    status/
      conditions.go
      conditions_test.go
      phase.go

  config/
    crd/
      bases/
        security.altlinux.org_altimageupdatepolicies.yaml

    default/
      kustomization.yaml
      manager_auth_proxy_patch.yaml
      manager_config_patch.yaml

    manager/
      kustomization.yaml
      manager.yaml

    rbac/
      altimageupdatepolicy_editor_role.yaml
      altimageupdatepolicy_viewer_role.yaml
      role.yaml
      role_binding.yaml
      service_account.yaml

    samples/
      security_v1alpha1_altimageupdatepolicy.yaml

    demo/
      namespace.yaml
      deployment.yaml
      context-configmap.yaml
      policy-always.yaml
      policy-alt-apt-simulation.yaml

  docs/
    demo.md
    troubleshooting.md
    architecture.md

  test/
    e2e/
      e2e_test.go
      kind_registry.sh
      manifests/
        deployment.yaml
        context-configmap.yaml
        policy-always.yaml

  hack/
    kind-with-registry.sh
    verify-codegen.sh
```

## 16.1. Key files

### `api/v1alpha1/altimageupdatepolicy_types.go`

Содержит:

* Spec structs;
* Status structs;
* Condition constants;
* Phase constants;
* Kubebuilder validation markers;
* Printer columns.

### `internal/controller/altimageupdatepolicy_controller.go`

Содержит основной reconcile.

Не должен содержать всю бизнес-логику. Он должен вызывать helper packages.

### `internal/jobs/check_job.go`

Создает Kubernetes Job для `AltAptSimulation`.

### `internal/jobs/build_job.go`

Создает Kubernetes Job для Kaniko.

### `internal/check/parser.go`

Парсит logs apt simulation.

### `internal/deploy/patch.go`

Готовит patch Deployment.

### `internal/deploy/rollout.go`

Проверяет rollout status.

### `internal/image/tag.go`

Генерирует tag и full image reference.

### `internal/run/build_id.go`

Генерирует run key и build id.

### `internal/status/conditions.go`

Управляет status conditions.

### `config/demo/`

Содержит манифесты для защиты ВКР.

---

# 17. Acceptance criteria

## AC-001. CRD установлен

Команда:

```bash
kubectl get crd altimageupdatepolicies.security.altlinux.org
```

возвращает CRD.

## AC-002. Controller запускается

Команда:

```bash
kubectl -n alt-image-update-operator-system get pods
```

показывает controller-manager в состоянии `Running`.

## AC-003. Policy создается

Команда:

```bash
kubectl apply -f config/demo/policy-always.yaml
```

успешно создает `AltImageUpdatePolicy`.

## AC-004. Always mode запускает Build Job

Для policy с `check.mode: Always` создается Build Job.

Проверка:

```bash
kubectl get jobs -n alt-update-demo -l security.altlinux.org/job-type=build
```

## AC-005. Build Job использует Kaniko

Build Job pod содержит container image Kaniko executor.

Проверка:

```bash
kubectl get job <build-job> -n alt-update-demo -o yaml
```

## AC-006. Build Job использует ConfigMap context

Build Job содержит volume из ConfigMap `demo-app-context` и mount `/workspace`.

## AC-007. Dockerfile выполняет ALT package update

Логи Build Job содержат выполнение:

```text
apt-get update
dist-upgrade
```

## AC-008. Image публикуется в registry

Registry tags endpoint показывает новый tag:

```bash
curl http://localhost:5001/v2/alt/demo-app/tags/list
```

## AC-009. Deployment обновляется

Image контейнера `app` в Deployment меняется на generated image.

Проверка:

```bash
kubectl get deploy demo-app -n alt-update-demo -o jsonpath='{.spec.template.spec.containers[?(@.name=="app")].image}'
```

## AC-010. Deployment annotations установлены

Deployment template содержит:

```text
security.altlinux.org/last-build-id
security.altlinux.org/last-built-image
security.altlinux.org/last-applied-at
security.altlinux.org/policy-name
security.altlinux.org/policy-namespace
```

## AC-011. Rollout успешен

Команда:

```bash
kubectl rollout status deployment/demo-app -n alt-update-demo
```

завершается успешно.

## AC-012. Status successful

CR status содержит:

```yaml
phase: Succeeded
conditions:
  - type: Ready
    status: "True"
```

## AC-013. Status image fields заполнены

CR status содержит непустые:

```yaml
lastBuiltImage:
lastAppliedImage:
```

И они равны image в Deployment.

## AC-014. Manual retrigger работает

После изменения `spec.trigger.manualToken` создается новая Build Job и новый image tag.

## AC-015. Повторный reconcile идемпотентен

Без изменения CR:

* новая Build Job не создается;
* Deployment не патчится повторно;
* phase остается `Succeeded`.

## AC-016. AltAptSimulation создает Check Job

Для policy с `check.mode: AltAptSimulation` создается Check Job.

Проверка:

```bash
kubectl get jobs -n alt-update-demo -l security.altlinux.org/job-type=check
```

## AC-017. Check Job выполняет apt simulation

Логи Check Job содержат:

```text
apt-get update
apt-get -s dist-upgrade
```

## AC-018. Ошибка target Deployment отражается в status

Если Deployment отсутствует:

```yaml
status:
  phase: Failed
  reason: TargetDeploymentNotFound
```

## AC-019. Ошибка target container отражается в status

Если container отсутствует:

```yaml
status:
  phase: Failed
  reason: TargetContainerNotFound
```

## AC-020. Ошибка ConfigMap отражается в status

Если ConfigMap отсутствует:

```yaml
status:
  phase: Failed
  reason: BuildContextNotFound
```

## AC-021. Ошибка Dockerfile key отражается в status

Если Dockerfile key отсутствует:

```yaml
status:
  phase: Failed
  reason: DockerfileKeyNotFound
```

## AC-022. Events создаются

Команда:

```bash
kubectl describe aiup demo-app-policy -n alt-update-demo
```

показывает события BuildStarted, BuildCompleted, DeploymentUpdated, RolloutCompleted.

## AC-023. Metrics доступны

Port-forward metrics endpoint:

```bash
kubectl -n alt-image-update-operator-system port-forward svc/alt-image-update-operator-controller-manager-metrics-service 8443:8443
```

или иной service, созданный Kubebuilder.

Endpoint содержит кастомные metrics:

```text
alt_image_update_build_total
alt_image_update_rollout_total
```

## AC-024. Unit tests проходят

Команда:

```bash
make test
```

успешно проходит unit/envtest набор.

## AC-025. E2E tests проходят

Команда:

```bash
make test-e2e
```

успешно выполняет kind/minikube сценарий полного pipeline.

---

# 18. Риски и ограничения

## 18.1. Риск: доступность ALT Linux base image

Описание:

* В разных окружениях имя ALT Linux image может отличаться.
* Image `alt:p10` может быть недоступен в конкретном registry setup.

Митигация:

* `spec.alt.baseImage` является обязательным параметром;
* demo manifests должны позволять быстро заменить image;
* docs должны описывать проверку `docker pull <baseImage>`.

## 18.2. Риск: apt команды отличаются между ветками

Описание:

* Поведение `apt-get` и формат вывода могут отличаться между `p10`, `p11`, `sisyphus`.

Митигация:

* parser должен быть простым и устойчивым к отсутствию конкретной строки;
* unit tests должны покрывать несколько вариантов логов;
* в status сохранять понятный reason при parse uncertainty.

## 18.3. Риск: local registry в kind требует настройки containerd

Описание:

* Build Job может успешно push image в registry, но node runtime не сможет pull image для Deployment.

Митигация:

* `docs/demo.md` должен содержать kind registry setup;
* e2e должен проверять не только push, но и rollout Deployment.

## 18.4. Риск: Kaniko image registry недоступен

Описание:

* `gcr.io/kaniko-project/executor` может быть недоступен из demo-сети.

Митигация:

* `spec.build.builderImage` позволяет переопределить image;
* docs должны описывать mirror или предварительную загрузку image.

## 18.5. Риск: ConfigMap context ограничен размером

Описание:

* Kubernetes ConfigMap имеет ограничение размера, поэтому ConfigMap context подходит только для demo и малых Dockerfile.

Митигация:

* MVP явно ограничивает context ConfigMap;
* Git context вынесен в follow-up.

## 18.6. Риск: нет security-only фильтра

Описание:

* `apt-get -s dist-upgrade` показывает все доступные обновления, не только security updates.

Митигация:

* в PRD зафиксировано как limitation MVP;
* тема ВКР фокусируется на применении обновлений безопасности, но MVP автоматизирует пересборку при доступных обновлениях пакетной базы;
* follow-up может добавить анализ advisory/security repository metadata.

## 18.7. Риск: dist-upgrade может менять набор пакетов

Описание:

* `dist-upgrade` может устанавливать или удалять пакеты.

Митигация:

* demo использует простой образ;
* Build Job failure явно отражается в status;
* для production follow-up можно добавить configurable update command.

## 18.8. Риск: rollout failure без rollback

Описание:

* Если новый image не стартует, Deployment может остаться в failed rollout.

Митигация:

* MVP фиксирует failure в status;
* rollback out-of-scope;
* manual rollback возможен через `kubectl rollout undo`.

## 18.9. Риск: registry credentials

Описание:

* Неправильный secret приведет к failure push.

Митигация:

* Build Job logs покажут ошибку Kaniko;
* status reason `BuildJobFailed`;
* troubleshooting docs должны содержать диагностику registry auth.

## 18.10. Риск: controller restart during pipeline

Описание:

* Controller может перезапуститься между созданием Job и обновлением status.

Митигация:

* deterministic job names;
* labels;
* reconcile восстанавливает состояние из Kubernetes API.

## 18.11. Ограничение: только Deployment

MVP не поддерживает StatefulSet, DaemonSet и другие workload types.

## 18.12. Ограничение: только one container per policy

Одна policy обновляет только один container в одном Deployment.

## 18.13. Ограничение: только namespaced resources

MVP не поддерживает target resources в другом namespace.

## 18.14. Ограничение: только Kaniko

BuildKit не реализуется в MVP.

---

# 19. Definition of Done для всего продукта

Продукт считается завершенным для MVP, если выполнены все условия:

## 19.1. API и CRD

* CRD `AltImageUpdatePolicy` создан.
* OpenAPI validation работает.
* Printer columns работают.
* Samples находятся в `config/samples` и `config/demo`.

## 19.2. Controller

* Reconcile реализует state machine.
* Check mode `Always` работает.
* Check mode `AltAptSimulation` работает.
* Build Job создается и отслеживается.
* Deployment patch реализован.
* Rollout observation реализован.
* Status conditions реализованы.
* Events реализованы.
* Metrics реализованы.

## 19.3. Реальная сборка

* Build Job использует Kaniko.
* Dockerfile берется из ConfigMap.
* Dockerfile использует ALT Linux base image.
* Dockerfile выполняет `apt-get update`.
* Dockerfile выполняет `apt-get -y dist-upgrade`.
* Итоговый image публикуется в registry.

## 19.4. Реальное развертывание

* Controller обновляет Deployment.
* Deployment создает новый ReplicaSet.
* Pod стартует с новым image.
* Rollout завершается успешно.
* CR status отражает итог.

## 19.5. Идемпотентность

* Повторный reconcile не создает дубликаты Jobs.
* Повторный reconcile не патчит Deployment бесконечно.
* Новый `manualToken` запускает новый run.
* Старые terminal states не вызывают новый run без изменения spec.

## 19.6. Тесты

* Unit tests проходят.
* Envtest tests проходят.
* E2E kind/minikube test проходит.
* E2E не использует fake build или fake status.

## 19.7. Документация

* `README.md` описывает назначение и quick start.
* `PRD.md` находится в корне.
* `docs/demo.md` содержит команды demo.
* `docs/troubleshooting.md` содержит диагностику:

    * registry недоступен;
    * Kaniko push failed;
    * Deployment pull failed;
    * Check Job failed;
    * ConfigMap missing;
    * rollout timeout.

## 19.8. Безопасность

* Нет Docker socket mount.
* Нет privileged Job containers.
* Registry credentials не логируются.
* Secret values не попадают в status.
* RBAC не использует cluster-admin.

## 19.9. ВКР demo

На чистом kind/minikube cluster можно выполнить demo и показать:

1. CRD;
2. operator pod;
3. `AltImageUpdatePolicy`;
4. Build Job;
5. Kaniko logs;
6. registry tags;
7. Deployment image update;
8. rollout success;
9. CR status `Succeeded`.

---

# Implementation notes for Codex agents

## Рекомендуемый порядок реализации

### Шаг 1. Bootstrap проекта

1. Создать Kubebuilder project.
2. Создать API `AltImageUpdatePolicy`.
3. Описать Spec/Status structs.
4. Добавить validation markers.
5. Сгенерировать CRD.
6. Проверить `make manifests` и `make generate`.

### Шаг 2. Pure helper packages

Реализовать и покрыть unit tests:

1. `internal/run/build_id.go`;
2. `internal/image/tag.go`;
3. `internal/check/parser.go`;
4. `internal/status/conditions.go`;
5. `internal/deploy/patch.go`;
6. `internal/deploy/rollout.go`;
7. `internal/jobs/names.go`.

Эти части проще всего проверить без cluster.

### Шаг 3. Job factories

Реализовать:

1. `internal/jobs/check_job.go`;
2. `internal/jobs/build_job.go`.

Проверить unit/envtest:

* labels;
* ownerReferences;
* volumes;
* mounts;
* Kaniko args;
* registrySecretRef behavior.

### Шаг 4. Controller preflight и status

Реализовать reconcile skeleton:

1. load policy;
2. compute run key/build id;
3. initialize status;
4. validate target Deployment;
5. validate ConfigMap/Dockerfile key;
6. write failure statuses.

### Шаг 5. Always mode pipeline

Сначала реализовать полный working path для `check.mode: Always`:

1. create/find Build Job;
2. wait Job complete;
3. patch Deployment;
4. wait rollout;
5. set `Succeeded`.

Это основной demo path.

### Шаг 6. AltAptSimulation mode

Добавить:

1. create/find Check Job;
2. wait complete;
3. read pod logs;
4. parse logs;
5. branch to `UpToDate` или build.

### Шаг 7. Observability

Добавить:

1. Events;
2. structured logs;
3. metrics.

### Шаг 8. Envtest

Добавить envtest сценарии:

* validation;
* missing target;
* missing ConfigMap;
* Always creates Build Job;
* idempotency;
* Deployment patch.

### Шаг 9. E2E

Добавить kind/minikube e2e:

1. local registry setup;
2. install CRD/operator;
3. apply demo manifests;
4. wait Build Job;
5. verify registry;
6. verify Deployment;
7. verify status.

### Шаг 10. Documentation

Подготовить:

1. README quick start;
2. `docs/demo.md`;
3. `docs/troubleshooting.md`;
4. финальные demo manifests.
