from pymilvus import Collection, connections, utility
import numpy as np
from typing import List, Optional
import os
import json
from .base import VectorDBAdapterBase

class MilvusAdapter(VectorDBAdapterBase):
    def __init__(self, collection_name: str = "semantic_cache"):
        connections.connect(
            alias="default", 
            host=os.getenv("MILVUS_HOST", "localhost"), 
            port=os.getenv("MILVUS_PORT", "19530")
        )
        
        self.collection_name = collection_name
        self._ensure_collection()

    def _ensure_collection(self):
        if not utility.has_collection(self.collection_name):
            from pymilvus import CollectionSchema, FieldSchema, DataType
            
            fields = [
                FieldSchema(name="id", dtype=DataType.INT64, is_primary=True, auto_id=True),
                FieldSchema(name="embedding", dtype=DataType.FLOAT_VECTOR, dim=384),
                FieldSchema(name="request_messages", dtype=DataType.VARCHAR, max_length=65535),
                FieldSchema(name="response_messages", dtype=DataType.VARCHAR, max_length=65535),
                FieldSchema(name="model", dtype=DataType.VARCHAR, max_length=64),
                FieldSchema(name="usage", dtype=DataType.VARCHAR, max_length=1024),
            ]
            
            schema = CollectionSchema(fields=fields, description="Semantic cache collection")
            self.collection = Collection(name=self.collection_name, schema=schema)
            
            index_params = {
                "metric_type": "IP",
                "index_type": "IVF_FLAT",
                "params": {"nlist": 1024}
            }
            self.collection.create_index(field_name="embedding", index_params=index_params)
        else:
            self.collection = Collection(self.collection_name)
            self.collection.load()

    def search(self, embedding: np.ndarray, model: str, similarity_threshold: float) -> Optional[dict]:
        search_params = {
            "metric_type": "IP",
            "params": {"nprobe": 10},
        }
        
        results = self.collection.search(
            data=[embedding.tolist()],
            anns_field="embedding",
            param=search_params,
            limit=1,
            expr=f'model == "{model}"'
        )

        if results[0].distances[0] >= similarity_threshold:
            return {
                "response_messages": json.loads(results[0].entity.response_messages),
                "similarity_score": float(results[0].distances[0]),
                "usage": json.loads(results[0].entity.usage)
            }
        return None

    def store(self, embedding: np.ndarray, request_messages: List[dict], 
              response_messages: List[dict], model: str, usage: dict):
        entities = [
            [embedding.tolist()],
            [json.dumps(request_messages)],
            [json.dumps(response_messages)],
            [model],
            [json.dumps(usage)]
        ]
        
        self.collection.insert([entities]) 