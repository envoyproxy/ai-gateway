from abc import ABC, abstractmethod
import numpy as np
from typing import List, Optional, Dict

class VectorDBAdapterBase(ABC):
    @abstractmethod
    def search(self, embedding: np.ndarray, model: str, similarity_threshold: float) -> Optional[dict]:
        pass

    @abstractmethod
    def store(self, embedding: np.ndarray, request_messages: List[dict], 
              response_messages: List[dict], model: str, usage: dict):
        pass 