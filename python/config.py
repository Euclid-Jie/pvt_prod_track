import os
from dotenv import load_dotenv

load_dotenv()

SQL_PASSWORDS = os.getenv("SQL_PASSWORDS")
SQL_HOST = os.getenv("SQL_HOST")
