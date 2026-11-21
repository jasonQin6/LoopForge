#!/usr/bin/env bash
# 跟进上游:rebase 三刀 patch → 跑受影响包测试 → (--deploy) Mac 构建 amd64 镜像中继 vol-server 并重启。
# 冲突或测试红即停,人工解;绝不自动部署(登录安全边界的 patch 必须人过目 diff)。
set -euo pipefail
cd "$(dirname "$0")/.."
BR=zcode-local-login
[ "$(git branch --show-current)" = "$BR" ] || { echo "checkout $BR first"; exit 1; }
git fetch https://github.com/Tencent/LoopForge.git main
BEHIND=$(git rev-list --count HEAD..FETCH_HEAD)
echo "upstream ahead by $BEHIND commits"
[ "$BEHIND" -gt 0 ] && git rebase FETCH_HEAD
# -skip 豁免已知环境底噪(旧部署 HEAD 同样红,与 patch 无关):前者在 Mac 非交互
# shell 固有红;后者的 seatbelt 断言按 linux CI 视角写,Mac 原生 shell 的 PATH 恒有
# sandbox-exec 必红。若新增红测必须停下人工看
go test ./internal/domain/ ./internal/runtime/ -count=1 -skip 'TestLocalWorkspaceGitDiscoveryCannotReachParent|TestProcessSandboxPathsProfilesAndCommands'
echo "rebase+tests OK; patches: $(git log --oneline FETCH_HEAD..HEAD | wc -l | tr -d ' ') commits"
if [ "${1:-}" = "--deploy" ]; then
  docker buildx build --platform linux/amd64 -t loopforge-platform:local --load . \
    && docker save --platform linux/amd64 loopforge-platform:local -o /tmp/lf-relay.tar \
    && scp /tmp/lf-relay.tar vol-server:/tmp/ \
    && ssh vol-server 'docker load -i /tmp/lf-relay.tar >/dev/null && rm /tmp/lf-relay.tar && cd ~/loopforge && git pull -q origin '"$BR"' 2>/dev/null || true; docker compose --profile platform -f compose.yaml -f compose.dev.yaml up -d --no-build --wait --wait-timeout 120' \
    && rm /tmp/lf-relay.tar && echo DEPLOYED
else
  echo "dry-run OK. deploy with: $0 --deploy"
fi
