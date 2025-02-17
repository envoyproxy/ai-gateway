import grpc
from concurrent import futures
import numpy as np
from typing import List
import logging
import routing_processor_pb2 as pb
import routing_processor_pb2_grpc as pb_grpc
from sentence_transformers import SentenceTransformer
from db_adapters.faiss_adapter import FAISSAdapter
from db_adapters.milvus_adapter import MilvusAdapter

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)

class SemanticCacheServicer(pb_grpc.SemanticCacheServiceServicer):
    def __init__(self, use_faiss: bool = True):
        self.embedding_model = SentenceTransformer('all-MiniLM-L6-v2')
        embedding_dim = self.embedding_model.get_sentence_embedding_dimension()
        
        if use_faiss:
            self.db = FAISSAdapter(dim=embedding_dim)
        else:
            self.db = MilvusAdapter()
        logger.info(f"Initialized SemanticCacheServicer with {'FAISS' if use_faiss else 'Milvus'} adapter")

    async def get_embedding(self, messages: List[pb.Message]) -> np.ndarray:
        text = " ".join([msg.content for msg in messages])
        embedding = self.embedding_model.encode(text, convert_to_numpy=True)
        return embedding.astype(np.float32)

    async def SearchCache(self, request: pb.SearchRequest, 
                         context: grpc.aio.ServicerContext) -> pb.SearchResponse:
        client = context.peer()
        logger.info(f"Received search request from {client}")
        
        embedding = await self.get_embedding(request.messages)
        
        result = self.db.search(
            embedding=embedding,
            model=request.model,
            similarity_threshold=request.similarity_threshold
        )
        
        if result:
            logger.info(f"Cache hit for {client} with similarity score: {result['similarity_score']}")
            return pb.SearchResponse(
                found=True,
                response_messages=[
                    pb.Message(**msg) for msg in result["response_messages"]
                ],
                similarity_score=result["similarity_score"],
                usage=pb.Usage(**result["usage"])
            )
        
        logger.info(f"Cache miss for {client}")
        return pb.SearchResponse(found=False)

    async def StoreChat(self, request: pb.StoreChatRequest, 
                       context: grpc.aio.ServicerContext) -> pb.StoreChatResponse:
        client = context.peer()
        logger.info(f"Received store request from {client}")
        
        try:
            embedding = await self.get_embedding(request.request_messages)
            
            self.db.store(
                embedding=embedding,
                request_messages=[{
                    "role": msg.role,
                    "content": msg.content,
                    "name": msg.name
                } for msg in request.request_messages],
                response_messages=[{
                    "role": msg.role,
                    "content": msg.content,
                    "name": msg.name
                } for msg in request.response_messages],
                model=request.model,
                usage={
                    "prompt_tokens": request.usage.prompt_tokens,
                    "completion_tokens": request.usage.completion_tokens,
                    "total_tokens": request.usage.total_tokens
                }
            )
            logger.info(f"Successfully stored chat for {client}")
            return pb.StoreChatResponse(success=True)
        except Exception as e:
            logger.error(f"Failed to store chat for {client}: {str(e)}")
            return pb.StoreChatResponse(success=False, error=str(e))

async def serve():
    server = grpc.aio.server(futures.ThreadPoolExecutor(max_workers=10))
    pb_grpc.add_SemanticCacheServiceServicer_to_server(
        SemanticCacheServicer(use_faiss=True),
        server
    )
    server.add_insecure_port('[::]:50052')
    logger.info("Starting server on [::]:50052")
    await server.start()
    logger.info("Server started successfully")
    await server.wait_for_termination()

if __name__ == '__main__':
    import asyncio
    asyncio.run(serve()) 