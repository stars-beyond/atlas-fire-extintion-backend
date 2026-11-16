import uuid
from datetime import datetime
from sqlalchemy import Column, String, ForeignKey, DateTime, JSON
from sqlalchemy.orm import relationship
from .database import Base

class Conversation(Base):
    __tablename__ = "conversations"
    id = Column(String, primary_key=True, default=lambda: str(uuid.uuid4()))
    title = Column(String, nullable=True)
    messages = relationship("Message", back_populates="conversation", order_by="Message.timestamp, Message.id")

class Message(Base):
    __tablename__ = "messages"
    id = Column(String, primary_key=True, default=lambda: str(uuid.uuid4()))
    content = Column(String, nullable=False)
    role = Column(String, nullable=False)
    timestamp = Column(DateTime, default=datetime.utcnow)
    sources = Column(JSON, nullable=True)
    conversation_id = Column(String, ForeignKey("conversations.id"))
    conversation = relationship("Conversation", back_populates="messages")

class TurnRequest(Base):
    __tablename__ = "turn_requests"
    id = Column(String, primary_key=True)
    conversation_id = Column(String, ForeignKey("conversations.id"), nullable=False)
    content = Column(String, nullable=False)
    status = Column(String, nullable=False, default="pending")
    detail = Column(String, nullable=True)
