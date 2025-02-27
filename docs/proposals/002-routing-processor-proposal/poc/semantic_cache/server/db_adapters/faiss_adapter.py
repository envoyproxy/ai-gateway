import faiss
import numpy as np
from typing import List, Optional, Dict, Any
import pickle
import os
from .base import VectorDBAdapterBase

class FAISSAdapter(VectorDBAdapterBase):
    def __init__(self, dim: int = 1536, index_file: str = "faiss_index.pkl"):
        self.index_file = index_file
        self.metadata_file = "faiss_metadata.pkl"
        
        if os.path.exists(index_file) and os.path.exists(self.metadata_file):
            self.load_index()
        else:
            self.index = faiss.IndexFlatIP(dim)  # Inner product similarity
            self.metadata: List[Dict[str, Any]] = []
            
        self.save_index()

    def load_index(self):
        try:
            with open(self.index_file, 'rb') as f:
                self.index = faiss.read_index(self.index_file)
        except FileNotFoundError:
            # Create a new index if file doesn't exist
            self.index = faiss.IndexFlatL2(self.dim)
            self.save_index()
        with open(self.metadata_file, 'rb') as f:
            self.metadata = pickle.load(f)

    def save_index(self):
        index_bytes = faiss.serialize_index(self.index)
        with open(self.index_file, 'wb') as f:
            f.write(index_bytes)
        with open(self.metadata_file, 'wb') as f:
            pickle.dump(self.metadata, f)

    def search(self, embedding: np.ndarray, model: str, similarity_threshold: float) -> Optional[dict]:
        if self.index.ntotal == 0:
            return None

        embedding = embedding.reshape(1, -1)
        distances, indices = self.index.search(embedding, 1)
        
        if distances[0][0] >= similarity_threshold:
            metadata = self.metadata[indices[0][0]]
            if metadata["model"] == model:
                return {
                    "response_messages": metadata["response_messages"],
                    "similarity_score": float(distances[0][0]),
                    "usage": metadata["usage"]
                }
        return None

    def store(self, embedding: np.ndarray, request_messages: List[dict], 
              response_messages: List[dict], model: str, usage: dict):
        self.index.add(embedding.reshape(1, -1))
        
        self.metadata.append({
            "request_messages": request_messages,
            "response_messages": response_messages,
            "model": model,
            "usage": usage
        })
        
        self.save_index() 