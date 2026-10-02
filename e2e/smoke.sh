#!/usr/bin/env bash
# ZFS 存储管理平台 e2e 冒烟(存储节点本机执行,root)
set -uo pipefail
M=http://127.0.0.1:8080/api/v1
INIT_IQN=$(grep -m1 "^InitiatorName" /etc/iscsi/initiatorname.iscsi | cut -d= -f2)
PASS=0; FAIL=0
IMG=/var/lib/zfs-platform/e2e-pool.img
LOOP=""
REQ_CODE=""; REQ_BODY=""
TOKEN=""
trap reset_e2e EXIT
reset_e2e() {
  iscsiadm -m node -T "${IQN:-}" -p "${PORTAL:-127.0.0.1:3260}" --logout >/dev/null 2>&1 || true
  umount /mnt/e2e >/dev/null 2>&1 || true
  [ -n "${LOOP:-}" ] && losetup -d "$LOOP" 2>/dev/null || true
  rm -f "$IMG"
  rm -rf /mnt/e2e
  # LIO 层残留(未解映射的 Target/backstore 会持有 zvol,必须先清)
  python3 /opt/zfs-platform/e2e/lio_cleanup.py >/dev/null 2>&1 || true
  # ZFS 残留自清理:仅限 e2e 自己的对象(绝不触碰 vol2 等用户资源)
  zfs destroy -r tank/vol-a 2>/dev/null || true
  zpool destroy -f e2epool 2>/dev/null || true
  zpool destroy -f e2epool 2>/dev/null || true
  iscsiadm -m node 2>/dev/null | grep -i ustc | awk '{print $2}' | while read -r t; do
    iscsiadm -m node -o delete -T "$t" -p 127.0.0.1 >/dev/null 2>&1 || true
  done
  # Manager 元数据复位:仅删除 e2e 测试命名空间(e2e-host / tank/vol-a / e2epool)
  sudo -u postgres psql -d zfsmgr -q -c "
    DELETE FROM mappings WHERE host_id IN (SELECT id FROM hosts WHERE name IN ('"'"'e2e-host'"'"'))
                          OR target_id IN (SELECT id FROM targets WHERE target_name LIKE '"'"'%vol-a%'"'"' OR target_name LIKE '"'"'%e2epool%'"'"');
    DELETE FROM initiators WHERE host_id IN (SELECT id FROM hosts WHERE name IN ('"'"'e2e-host'"'"'));
    DELETE FROM hosts WHERE name IN ('"'"'e2e-host'"'"');
    DELETE FROM luns WHERE target_id IN (SELECT id FROM targets WHERE target_name LIKE '"'"'%vol-a%'"'"' OR target_name LIKE '"'"'%e2epool%'"'"');
    DELETE FROM acls WHERE target_id IN (SELECT id FROM targets WHERE target_name LIKE '"'"'%vol-a%'"'"' OR target_name LIKE '"'"'%e2epool%'"'"');
    DELETE FROM targets WHERE target_name LIKE '"'"'%vol-a%'"'"' OR target_name LIKE '"'"'%e2epool%'"'"';
    DELETE FROM datasets WHERE name='"'"'tank/vol-a'"'"';
    DELETE FROM pools WHERE name='"'"'e2epool'"'"';" 2>/dev/null || true
}


reset_e2e   # 运行前清理上一轮残留

ok()   { PASS=$((PASS+1)); echo "  PASS  $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL  $1"; }

req() { # req METHOD PATH [json]  -> 全局 REQ_CODE / REQ_BODY
  local m=$1 p=$2 body=${3:-}
  if [ -n "$body" ]; then
    REQ_BODY=$(curl -s -X "$m" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
          -d "$body" -w '\n%{http_code}' "$M$p")
  else
    REQ_BODY=$(curl -s -X "$m" -H "Authorization: Bearer $TOKEN" -w '\n%{http_code}' "$M$p")
  fi
  REQ_CODE=${REQ_BODY##*$'\n'}
  REQ_BODY=${REQ_BODY%$'\n'*}
}
get() { # get PATH  -> REQ_CODE/REQ_BODY
  REQ_BODY=$(curl -s -H "Authorization: Bearer $TOKEN" -w '\n%{http_code}' "$M$1")
  REQ_CODE=${REQ_BODY##*$'\n'}
  REQ_BODY=${REQ_BODY%$'\n'*}
}
j()  { echo "$REQ_BODY" | jq -r "$1" 2>/dev/null; }
je() { echo "$REQ_BODY" | jq -e "$1" >/dev/null 2>&1; }

echo "== ZFS 平台 e2e 冒烟开始 $(date +%T) =="
echo "1) 登录 & 基础"
# login helper / req2 helper(指定令牌)
req2() { # req2 METHOD PATH [json] token
  local m=$1 p=$2 body=${3:-} t=$4
  if [ -n "$body" ]; then
    REQ_BODY=$(curl -s -X "$m" -H "Authorization: Bearer $t" -H 'Content-Type: application/json' -d "$body" -w '\n%{http_code}' "$M$p")
  else
    REQ_BODY=$(curl -s -X "$m" -H "Authorization: Bearer $t" -w '\n%{http_code}' "$M$p")
  fi
  REQ_CODE=${REQ_BODY##*$'\n'}; REQ_BODY=${REQ_BODY%$'\n'*}
}
login() { curl -s -X POST "$M/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"$1\",\"password\":\"$2\"}" | jq -r '.data.token'; }

ADMIN=$(login admin admin123); TOKEN=$ADMIN
[ -n "$ADMIN" ] && [ "$ADMIN" != "null" ] && ok "admin 登录" || bad "admin 登录"

for i in $(seq 1 30); do
  get /nodes
  [ "$(j '.data.nodes[]? | select(.name=="node1") | .online')" = "true" ] && break
  sleep 2
done
[ "$(j '.data.nodes[]? | select(.name=="node1") | .online')" = "true" ] && ok "node1 上线" || bad "node1 上线(心跳)"
NODE_ID=$(j '.data.nodes[]|select(.name=="node1")|.id')

get /pools; je '.data.pools[]|select(.name=="tank" and .health=="ONLINE")' \
  && ok "发现既有池 tank(自动入库)" || bad "发现既有池 tank"
PID=$(j '.data.pools[]|select(.name=="tank")|.id')

echo "1.5) API 预清理(删除 tank/vol-a 幽灵行与 e2e 池行,防轮询复活)"
get /datasets
for rid in $(echo "$REQ_BODY" | jq -r '.data.datasets[]? | select(.name=="tank/vol-a") | .id'); do
  req DELETE "/datasets/$rid" '{"confirm":"vol-a"}' 2>/dev/null || true
  sudo -u postgres psql -d zfsmgr -q -c "DELETE FROM datasets WHERE id=$rid;" 2>/dev/null || true
done
get /pools
for rid in $(echo "$REQ_BODY" | jq -r '.data.pools[]? | select(.name=="e2epool") | .id'); do
  sudo -u postgres psql -d zfsmgr -q -c "DELETE FROM pools WHERE id=$rid;" 2>/dev/null || true
done
sleep 13   # 越过一次轮询窗口,确保 DB 与实况一致

echo "2) RBAC"
VTOKEN=$(login viewer viewer123); OTOKEN=$(login operator operator123)
req2 POST /pools '{}' "$VTOKEN";  [ "$REQ_CODE" = "403" ] && ok "viewer 写被拒 403" || bad "viewer 写被拒 (得 $REQ_CODE)"
req2 POST /users '{"username":"x","password":"123456","role":"admin"}' "$OTOKEN"
[ "$REQ_CODE" = "403" ] && ok "operator 用户管理被拒 403" || bad "operator 用户管理 (得 $REQ_CODE)"
req2 GET /pools "" "$OTOKEN"; [ "$REQ_CODE" = "200" ] && ok "operator 只读 200" || bad "operator 只读 (得 $REQ_CODE)"
req2 GET /pools "" "$VTOKEN"; [ "$REQ_CODE" = "200" ] && ok "viewer 只读 200" || bad "viewer 只读 (得 $REQ_CODE)"

echo "3) 建池(loop 盘)→scrub→销毁"
truncate -s 400M "$IMG"; LOOP=$(losetup -f); losetup "$LOOP" "$IMG"
TOKEN=$ADMIN
req POST /pools "{\"node_id\":$NODE_ID,\"name\":\"e2epool\",\"disks\":[\"$LOOP\"]}"
[ "$REQ_CODE" = "200" ] && ok "创建池 e2epool" || bad "创建池 ($REQ_CODE $(j .error.message))"
POOL_ID=$(j '.data.pool_id')
req POST "/pools/$POOL_ID/scrub" '{"action":"start"}'
[ "$REQ_CODE" = "200" ] && ok "启动 scrub" || bad "启动 scrub ($REQ_CODE)"
sleep 3
req POST "/pools/$POOL_ID/scrub" '{"action":"stop"}'
[ "$REQ_CODE" = "200" ] && ok "停止 scrub" || bad "停止 scrub ($REQ_CODE)"
req DELETE "/pools/$POOL_ID" '{"confirm":"e2epool"}'
[ "$REQ_CODE" = "200" ] && ok "销毁池 e2epool" || bad "销毁池 ($REQ_CODE $(j .error.message))"
losetup -d "$LOOP"; LOOP=""

echo "4) zvol 生命周期"
req POST /datasets "{\"node_id\":$NODE_ID,\"pool_id\":$PID,\"name\":\"vol-a\",\"size\":\"2G\",\"compression\":\"lz4\",\"sparse\":true}"
[ "$REQ_CODE" = "200" ] && ok "创建 zvol tank/vol-a 2G" || bad "创建 zvol ($REQ_CODE $(j .error.message))"
VOL_ID=$(j '.data.dataset.id')
get /datasets; je ".data.datasets[]|select(.name==\"tank/vol-a\" and .mapped==false)" \
  && ok "zvol 出现在列表" || bad "zvol 列表"

echo "5) 格式化 ext4"
req POST "/datasets/$VOL_ID/format" '{"filesystem":"ext4","confirm":"vol-a"}'
[ "$REQ_CODE" = "200" ] && ok "格式化 ext4" || bad "格式化 ext4 ($REQ_CODE $(j .error.message))"
get "/datasets/$VOL_ID"
je '.data.filesystem.type=="ext4" and .data.filesystem.status=="ready"' \
  && ok "filesystem=ext4/ready" || bad "filesystem 状态 ($(j .data.filesystem))"
req POST "/datasets/$VOL_ID/format" '{"filesystem":"xfs","confirm":"WRONG"}'
[ "$REQ_CODE" = "400" ] && ok "错误确认码被拒(400)" || bad "错误确认码 (得 $REQ_CODE)"

echo "6) 扩容"
req PATCH "/datasets/$VOL_ID/resize" '{"size":"3G"}'
[ "$REQ_CODE" = "200" ] && ok "扩容 2G→3G" || bad "扩容 ($REQ_CODE $(j .error.message))"

echo "7) 快照/回滚/删除"
req POST "/datasets/$VOL_ID/snapshots" '{"name":"e2e-s1"}'
[ "$REQ_CODE" = "200" ] && ok "创建快照 e2e-s1" || bad "创建快照 ($REQ_CODE $(j .error.message))"
get "/datasets/$VOL_ID/snapshots"; SNAP_ID=$(j '.data.snapshots[0].id')
req POST "/datasets/$VOL_ID/rollback" '{"snapshot":"e2e-s1","confirm":"vol-a"}'
[ "$REQ_CODE" = "200" ] && ok "回滚到 e2e-s1" || bad "回滚 ($REQ_CODE $(j .error.message))"
req DELETE "/snapshots/$SNAP_ID"
[ "$REQ_CODE" = "200" ] && ok "删除快照" || bad "删除快照 ($REQ_CODE)"
get "/datasets/$VOL_ID/snapshots"; je '.data.snapshots|length==0' && ok "快照清空" || bad "快照清空"

echo "8) 主机档案 + CHAP 映射"
req POST /hosts '{"name":"e2e-host","os_type":"Debian","description":"e2e"}'
HID=$(j '.data.host_id')
[ "$REQ_CODE" = "200" ] && ok "建主机 e2e-host" || bad "建主机"
req POST "/hosts/$HID/initiators" "{\"iqn\":\"$INIT_IQN\",\"chap_user\":\"e2euser\",\"chap_secret\":\"e2e-secret-123456\"}"
[ "$REQ_CODE" = "200" ] && ok "录入本机 Initiator(CHAP)" || bad "录入 Initiator ($REQ_CODE $(j .error.message))"
req POST /mappings "{\"host_id\":$HID,\"dataset_id\":$VOL_ID}"
[ "$REQ_CODE" = "200" ] && ok "映射 vol-a → e2e-host(自动建 Target+ACL)" || bad "创建映射 ($REQ_CODE $(j .error.message))"
IQN=$(j '.data.iqn'); PORTAL=$(j '.data.portal')
get /mappings; je ".data.mappings[]|select(.host_id==$HID and .dataset==\"tank/vol-a\")" \
  && ok "映射记录存在" || bad "映射记录存在"
get /datasets; je '.data.datasets[]|select(.name=="tank/vol-a" and .mapped==true)' \
  && ok "卷标记已映射" || bad "卷标记已映射"
req POST "/datasets/$VOL_ID/format" '{"filesystem":"xfs","confirm":"vol-a"}'
[ "$REQ_CODE" = "409" ] && ok "已映射卷格式化被拒(409)" || bad "已映射卷格式化被拒 (得 $REQ_CODE)"

echo "9) 本机 initiator 经 CHAP 登录 → 真实读写"
iscsiadm -m discovery -t sendtargets -p "$PORTAL" >/dev/null 2>&1
iscsiadm -m node -T "$IQN" -p "$PORTAL" -o update -n node.session.auth.authmethod -v CHAP
iscsiadm -m node -T "$IQN" -p "$PORTAL" -o update -n node.session.auth.username -v e2euser
iscsiadm -m node -T "$IQN" -p "$PORTAL" -o update -n node.session.auth.password -v wrong-password-000
iscsiadm -m node -T "$IQN" -p "$PORTAL" --login >/dev/null 2>&1 \
  && { bad "错误 CHAP 口令应失败"; iscsiadm -m node -T "${IQN:-}" -p "${PORTAL:-127.0.0.1:3260}" --logout >/dev/null 2>&1; } \
  || ok "错误 CHAP 口令被拒"
iscsiadm -m node -T "$IQN" -p "$PORTAL" -o update -n node.session.auth.password -v 'e2e-secret-123456'
iscsiadm -m node -T "$IQN" -p "$PORTAL" --login >/dev/null 2>&1 && ok "正确 CHAP 登录成功" || bad "正确 CHAP 登录"
sleep 2
DEV=/dev/disk/by-path/ip-${PORTAL}-iscsi-${IQN}-lun-0
for i in $(seq 1 15); do
  [ -L "$DEV" ] && break
  sleep 1
done
[ -L "$DEV" ] && ok "LUN 设备出现" || bad "LUN 设备出现 ($DEV)"
for i in $(seq 1 10); do
  get "/nodes/$NODE_ID/sessions"
  je ".data.sessions[]|select(.target_name==\"$IQN\" and .iqn==\"$INIT_IQN\")" && break
  sleep 1
done
je ".data.sessions[]|select(.target_name==\"$IQN\" and .iqn==\"$INIT_IQN\")" \
  && ok "平台可见活动会话" || bad "平台可见活动会话 ($(j .data.sessions))"
mkdir -p /mnt/e2e
mount "$DEV" /mnt/e2e 2>/dev/null && ok "挂载 ext4 卷" || bad "挂载 ext4 卷"
head -c 8M /dev/urandom > /mnt/e2e/data.bin
sha1=$(sha1sum /mnt/e2e/data.bin | cut -d' ' -f1)
sync; umount /mnt/e2e
mount "$DEV" /mnt/e2e 2>/dev/null
sha2=$(sha1sum /mnt/e2e/data.bin | cut -d' ' -f1)
[ "$sha1" = "$sha2" ] && ok "数据往返一致 (sha1=$sha1)" || bad "数据往返一致"
umount /mnt/e2e
iscsiadm -m node -T "${IQN:-}" -p "${PORTAL:-127.0.0.1:3260}" --logout >/dev/null 2>&1 && ok "登出" || bad "登出"
sleep 2
get "/nodes/$NODE_ID/sessions"; je '.data.sessions|length==0' && ok "登出后会话清空" || bad "会话未清空"

echo "10) 解映射(自动清理专用 Target)"
get "/mappings?host_id=$HID"; MID=$(j '.data.mappings[0].id')
req DELETE "/mappings/$MID"
[ "$REQ_CODE" = "200" ] && ok "解除映射" || bad "解除映射 ($REQ_CODE $(j .error.message))"
get /targets; je "[.data.targets[]? | select(.target_name==\"$IQN\")] | length == 0" \
  && ok "专用 Target 已清理" || bad "Target 未清理 → 现存: $(j -c .data.targets)"
get /datasets; je '.data.datasets[]|select(.name=="tank/vol-a" and .mapped==false)' \
  && ok "卷回到未映射且 FS 保留(ext4)" || bad "卷状态"

echo "11) 审计留痕"
get "/audit-logs?username=operator"; je '.data.audit_logs|length>=4' \
  && ok "审计含 operator 操作" || bad "审计行数 $(j '.data.audit_logs|length')"

echo "12) 清理 e2e 卷/主机"
req DELETE "/datasets/$VOL_ID" '{"confirm":"vol-a"}'
[ "$REQ_CODE" = "200" ] && ok "删除 tank/vol-a" || bad "删除卷 ($REQ_CODE $(j .error.message))"
req DELETE "/hosts/$HID"
[ "$REQ_CODE" = "200" ] && ok "删除主机档案" || bad "删除主机 ($REQ_CODE)"

echo
echo "==== 结果: PASS=$PASS FAIL=$FAIL ===="
[ "$FAIL" = "0" ]
