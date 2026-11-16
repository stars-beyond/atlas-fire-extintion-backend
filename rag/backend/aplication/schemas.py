from datetime import datetime
from typing import Literal
from uuid import UUID, uuid4
from pydantic import BaseModel, ConfigDict, Field, field_validator

class Source(BaseModel):
    citation: int
    document_id: str | None = None
    title: str | None = None
    experiment: str | None = None
    page: int | None = None
    row: int | None = None
    evidence_level: str | None = None
    source_type: str | None = None
    source: str | None = None
    authors: str | list[str] | None = None
    doi: str | None = None
    url: str | None = None
    download_url: str | None = None
    local_url: str | None = None
    publication_date: str | None = None
    retrieval_score: float | None = None
    retrieval_method: str | None = None
    cited: bool | None = None
    formula_validation: str | None = None
    excerpt: str | None = None
    data_warnings: list[str] = Field(default_factory=list)

class MessageSend(BaseModel):
    content: str = Field(min_length=1, max_length=2000)
    request_id: UUID = Field(default_factory=uuid4)
    @field_validator("content")
    @classmethod
    def strip_content(cls, value):
        if not value.strip():
            raise ValueError("Please write a question.")
        return value.strip()

class Message(BaseModel):
    model_config = ConfigDict(from_attributes=True)
    id: str
    role: Literal["user", "assistant"]
    content: str
    timestamp: datetime
    sources: list[Source] = Field(default_factory=list)
    confidence: float | None = None

class MessageResponse(BaseModel):
    user_message: Message
    ai_message: Message

class ConversationSummary(BaseModel):
    model_config = ConfigDict(from_attributes=True)
    id: str
    title: str | None = None
    timestamp: datetime | None = None

class Conversation(BaseModel):
    id: str
    title: str | None = None
    messages: list[Message] = Field(default_factory=list)
