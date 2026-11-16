import os
from sqlalchemy import create_engine
from sqlalchemy.orm import declarative_base, sessionmaker
from backend.flame.config import ROOT

(ROOT / "data").mkdir(exist_ok=True)
DATABASE_URL = os.getenv("DATABASE_URL") or "sqlite:///" + (ROOT / "data/chat.db").as_posix()
engine = create_engine(DATABASE_URL, connect_args={"check_same_thread": False} if DATABASE_URL.startswith("sqlite") else {}, pool_pre_ping=True)
SessionLocal = sessionmaker(autocommit=False, autoflush=False, bind=engine)
Base = declarative_base()

def get_db():
    with SessionLocal() as db:
        yield db
