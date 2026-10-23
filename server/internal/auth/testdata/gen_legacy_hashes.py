"""生成旧哈希兼容测试用的样本（password_test.go 里的 legacyHash* 常量）。

参数与旧版 server/server.py 的 PasswordHasher 完全一致。密码是专门的测试密码，
不涉及旧 users.db 里的任何真实数据。每次运行盐都不同，结果不必与常量逐字相同。

    python -m venv venv && venv/Scripts/pip install argon2-cffi
    venv/Scripts/python gen_legacy_hashes.py
"""
from argon2 import PasswordHasher

legacy = PasswordHasher(time_cost=3, memory_cost=64 * 1024, parallelism=2, hash_len=32, salt_len=16)
print("legacy      :", legacy.hash("legacy-Passw0rd!"))
print("legacy-utf8 :", legacy.hash("旧版密码-测试"))
# argon2-cffi 25.x 的默认参数（parallelism=4），用来测"参数不同 → 登录后自动重新哈希"
print("cffi-default:", PasswordHasher().hash("legacy-Passw0rd!"))
