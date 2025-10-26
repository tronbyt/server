#!/usr/bin/env python3
"""Reset admin password script - can be run inside the container or locally."""

import sys
import os

# Add the app directory to the path
sys.path.insert(0, '/app' if os.path.exists('/app') else '.')

import sqlite3
import json
from werkzeug.security import generate_password_hash

# Try to find the database file
db_path = None
for path in ['users/usersdb.sqlite', '/app/users/usersdb.sqlite']:
    if os.path.exists(path):
        db_path = path
        break

if not db_path:
    print("ERROR: Could not find usersdb.sqlite")
    sys.exit(1)

print(f"Using database: {db_path}")

conn = sqlite3.connect(db_path)
cursor = conn.cursor()

cursor.execute('SELECT username, data FROM json_data WHERE username = ?', ('admin',))
row = cursor.fetchone()

if row:
    user_data = json.loads(row[1])
    new_password = 'password'
    user_data['password'] = generate_password_hash(new_password)
    
    cursor.execute('UPDATE json_data SET data = ? WHERE username = ?', 
                   (json.dumps(user_data), 'admin'))
    conn.commit()
    print(f'Password reset successful!')
    print(f'Username: admin')
    print(f'Password: {new_password}')
else:
    print('ERROR: Admin user not found in database')
    sys.exit(1)
    
conn.close()
