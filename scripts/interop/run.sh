#!/bin/bash
# Interoperability checks with third-party clients (lftp over FTP, FTPS and
# SFTP; rclone over FTP, FTPS and SFTP; paramiko over SFTP). Run it through
# `make interop`, which builds the client image and uses the host network.
#
# Needs a running server and a user without sign-in restrictions or a tight
# quota. Settings come from the environment:
#   KERVAN_HOST (127.0.0.1)  FTP_PORT (2121)  FTPS_PORT (990, implicit)
#   SFTP_PORT (2222)  KERVAN_USER  KERVAN_PASSWORD
# FTPS checks need ftps.enabled with mode "both"; set FTPS_PORT=0 to skip
# implicit FTPS. The server's per-IP connection limit must allow at least
# 6 connections (rclone and parallel lftp mirrors).
set -u
H=${KERVAN_HOST:-127.0.0.1}; FTP=${FTP_PORT:-2121}; FTPS=${FTPS_PORT:-990}; SFTP=${SFTP_PORT:-2222}
U=${KERVAN_USER:?set KERVAN_USER}; P=${KERVAN_PASSWORD:?set KERVAN_PASSWORD}
export U P H SFTP
pass=0; fail=0
check() { local name=$1; shift; if "$@" >/tmp/out 2>&1; then echo "PASS $name"; pass=$((pass+1)); else echo "FAIL $name"; tail -5 /tmp/out | sed 's/^/     /'; fail=$((fail+1)); fi; }

mkdir -p /w/src/sub /w/dl && cd /w
head -c 5000000 /dev/urandom > src/big.bin; echo hello > src/a.txt; echo nested > src/sub/n.txt
same() { diff -r /w/src "$1"; }

LFTP_FTP="set ftp:ssl-allow no; set ftp:passive-mode on; set net:max-retries 1; set net:timeout 10"
LFTP_TLS="set ftp:passive-mode on; set net:max-retries 1; set net:timeout 10; set ftp:ssl-allow yes; set ftp:ssl-force true; set ftp:ssl-protect-data true; set ssl:verify-certificate no"

# --- lftp, plain FTP
check "lftp ftp: mirror upload"   lftp -u "$U,$P" -e "$LFTP_FTP; mirror -R src interop-ftp; bye" ftp://$H:$FTP
check "lftp ftp: mirror download" lftp -u "$U,$P" -e "$LFTP_FTP; mirror interop-ftp /w/dl/ftp; bye" ftp://$H:$FTP
check "lftp ftp: content equal"   same /w/dl/ftp
check "lftp ftp: resume (get -c)" bash -c "head -c 1000000 src/big.bin > /w/dl/part.bin && lftp -u '$U,$P' -e '$LFTP_FTP; get -c interop-ftp/big.bin -o /w/dl/part.bin; bye' ftp://$H:$FTP && cmp /w/dl/part.bin src/big.bin"
check "lftp ftp: mv/mkdir/rm/rmdir" lftp -u "$U,$P" -e "$LFTP_FTP; mkdir interop-ftp/x; mv interop-ftp/a.txt interop-ftp/x/a.txt; cls interop-ftp/x/a.txt; rm interop-ftp/x/a.txt; rmdir interop-ftp/x; bye" ftp://$H:$FTP
check "lftp ftp: parallel get"    lftp -u "$U,$P" -e "$LFTP_FTP; mirror --parallel=4 interop-ftp /w/dl/par; bye" ftp://$H:$FTP

# --- lftp, explicit FTPS and implicit FTPS
check "lftp ftps explicit: mirror up+down" bash -c "lftp -u '$U,$P' -e '$LFTP_TLS; mirror -R src interop-ftpes; mirror interop-ftpes /w/dl/ftpes; bye' ftp://$H:$FTP && diff -r src /w/dl/ftpes"
[ "$FTPS" != 0 ] && check "lftp ftps implicit: mirror up+down" bash -c "lftp -u '$U,$P' -e 'set ssl:verify-certificate no; set net:max-retries 1; mirror -R src interop-ftps; mirror interop-ftps /w/dl/ftps; bye' ftps://$H:$FTPS && diff -r src /w/dl/ftps"

# --- lftp over SFTP (uses the OpenSSH client underneath)
check "lftp sftp: mirror up+down" bash -c "lftp -e 'set sftp:auto-confirm yes; set sftp:connect-program \"ssh -a -x -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null\"; mirror -R src interop-lsftp; mirror interop-lsftp /w/dl/lsftp; bye' -u '$U,$P' sftp://$H:$SFTP && diff -r src /w/dl/lsftp"

# --- rclone (FTP, FTPS, SFTP backends)
OBS=$(rclone obscure "$P")
export RCLONE_CONFIG=/w/rclone.conf
cat > $RCLONE_CONFIG <<CONF
[kftp]
type = ftp
host = $H
port = $FTP
user = $U
pass = $OBS

[kftpes]
type = ftp
host = $H
port = $FTP
user = $U
pass = $OBS
explicit_tls = true
no_check_certificate = true

[ksftp]
type = sftp
host = $H
port = $SFTP
user = $U
pass = $OBS
shell_type = none
CONF
for r in kftp kftpes ksftp; do
  check "rclone $r: copy up" rclone copy src $r:interop-$r --transfers 4
  check "rclone $r: check"   rclone check src $r:interop-$r --size-only
  check "rclone $r: copy down + diff" bash -c "rclone copy $r:interop-$r /w/dl/$r && diff -r src /w/dl/$r"
  check "rclone $r: moveto + delete" bash -c "rclone moveto $r:interop-$r/a.txt $r:interop-$r/moved.txt && rclone deletefile $r:interop-$r/moved.txt && ! rclone lsf $r:interop-$r | grep -q moved"
  check "rclone $r: purge" rclone purge $r:interop-$r
done
check "rclone sftp: about (statvfs)" rclone about ksftp:

# --- paramiko
check "paramiko: full SFTP session" python3 - <<'PY'
import os, paramiko, stat
t = paramiko.Transport((os.environ["H"], int(os.environ["SFTP"]))); t.connect(username=os.environ["U"], password=os.environ["P"])
s = paramiko.SFTPClient.from_transport(t)
s.mkdir("pm"); s.put("/w/src/big.bin", "pm/big.bin")
assert s.stat("pm/big.bin").st_size == 5000000
s.get("pm/big.bin", "/w/dl/pm.bin"); assert open("/w/dl/pm.bin","rb").read() == open("/w/src/big.bin","rb").read()
with s.open("pm/w.txt", "w") as f: f.write(b"abc"); f.flush()
with s.open("pm/w.txt", "a") as f: f.write(b"def")
assert s.open("pm/w.txt").read() == b"abcdef"
s.posix_rename("pm/w.txt", "pm/big.bin")           # overwrite via extension
assert s.stat("pm/big.bin").st_size == 6
s.rename("pm/big.bin", "pm/r.txt")
s.utime("pm/r.txt", (1600000000, 1600000000)); assert int(s.stat("pm/r.txt").st_mtime) == 1600000000
assert [e.filename for e in s.listdir_attr("pm")] == ["r.txt"]
assert stat.S_ISDIR(s.stat("pm").st_mode)
assert s.normalize(".") == "/"
s.remove("pm/r.txt"); s.rmdir("pm")
try:
    s.stat("pm"); raise SystemExit("rmdir failed")
except FileNotFoundError: pass
t.close()
PY

# cleanup
lftp -u "$U,$P" -e "$LFTP_FTP; rm -r interop-ftp interop-ftpes interop-ftps interop-lsftp; bye" ftp://$H:$FTP >/dev/null 2>&1
echo "TOTAL pass=$pass fail=$fail"
[ "$fail" -eq 0 ]
