import grpc
from concurrent import futures
import numpy as np
from typing import List, Dict
import logging
import uuid
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

        # In-memory storage for pending requests
        self.pending_searches: Dict[str, np.ndarray] = {}
        self.pending_stores: Dict[str, Dict] = {}

        logger.info(f"Initialized SemanticCacheServicer with {'FAISS' if use_faiss else 'Milvus'} adapter")

    async def get_embedding(self, messages: List[pb.Message]) -> np.ndarray:
        text = " ".join([msg.content for msg in messages])
        embedding = self.embedding_model.encode(text, convert_to_numpy=True)
        return embedding.astype(np.float32)

    async def SearchCache(self, request: pb.SearchRequest, 
                         context: grpc.aio.ServicerContext) -> pb.SearchResponse:
        client = context.peer()
        logger.info(f"Received search request from {client}, messages: {request.messages}")
        
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

    async def InitiateCacheSearch(self, request: pb.InitiateCacheSearchRequest,
                                context: grpc.aio.ServicerContext) -> pb.InitiateCacheSearchResponse:
        client = context.peer()
        logger.info(f"Received initiate search request from {client}, messages: {request.messages}")

        # Generate unique request ID
        request_id = str(uuid.uuid4())

        # Calculate and store embedding
        embedding = await self.get_embedding(request.messages)
        self.pending_searches[request_id] = {
            'embedding': embedding,
            'model': request.model,
            'similarity_threshold': request.similarity_threshold
        }

        logger.info(f"Stored search request with ID: {request_id}")
        return pb.InitiateCacheSearchResponse(request_id=request_id)

    async def CompleteCacheSearch(self, request: pb.CompleteCacheSearchRequest,
                                context: grpc.aio.ServicerContext) -> pb.SearchResponse:
        client = context.peer()
        logger.info(f"Received complete search request from {client} for ID: {request.request_id}")

        # Retrieve stored search request
        search_data = self.pending_searches.pop(request.request_id, None)
        if not search_data:
            logger.error(f"No pending search found for ID: {request.request_id}")
            return pb.SearchResponse(
                found=False,
                error="Invalid or expired request ID"
            )

        # Perform search
        result = self.db.search(
            embedding=search_data['embedding'],
            model=search_data['model'],
            similarity_threshold=search_data['similarity_threshold']
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

    async def InitiateCacheStore(self, request: pb.InitiateCacheStoreRequest,
                               context: grpc.aio.ServicerContext) -> pb.InitiateCacheStoreResponse:
        client = context.peer()
        logger.info(f"Received initiate store request from {client}")

        # Generate unique request ID
        request_id = str(uuid.uuid4())

        # Calculate and store embedding and request data
        embedding = await self.get_embedding(request.request_messages)
        self.pending_stores[request_id] = {
            'embedding': embedding,
            'request_messages': [{
                "role": msg.role,
                "content": msg.content,
                "name": msg.name
            } for msg in request.request_messages],
            'model': request.model
        }

        logger.info(f"Stored store request with ID: {request_id}")
        return pb.InitiateCacheStoreResponse(request_id=request_id)

    async def CompleteCacheStore(self, request: pb.CompleteCacheStoreRequest,
                               context: grpc.aio.ServicerContext) -> pb.StoreChatResponse:
        client = context.peer()
        logger.info(f"Received complete store request from {client} for ID: {request.request_id}")

        # Retrieve stored store request
        store_data = self.pending_stores.pop(request.request_id, None)
        if not store_data:
            logger.error(f"No pending store found for ID: {request.request_id}")
            return pb.StoreChatResponse(
                success=False,
                error="Invalid or expired request ID"
            )

        try:
            # Store in database
            self.db.store(
                embedding=store_data['embedding'],
                request_messages=store_data['request_messages'],
                response_messages=[{
                    "role": msg.role,
                    "content": msg.content,
                    "name": msg.name
                } for msg in request.response_messages],
                model=store_data['model'],
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

class RoutingProcessorServicer(pb_grpc.RoutingProcessorServicer):
    async def GetCapabilities(self, request: pb.CapabilitiesRequest,
                            context: grpc.aio.ServicerContext) -> pb.CapabilitiesResponse:
        """Return the capabilities of this routing processor implementation."""
        logger.info("Received capabilities request for RoutingProcessor")
        return pb.CapabilitiesResponse(
            stateless_semantic_cache_supported=True,
            stateful_semantic_cache_supported=True,
            model_selection_supported=False,
            immediate_response_supported=True
        )

    async def ExternalProcess(self, request_iterator, context):
        # Placeholder for future implementation
        raise NotImplementedError("ExternalProcess not yet implemented")

async def serve():
    server = grpc.aio.server(futures.ThreadPoolExecutor(max_workers=10))
    semantic_cache_servicer = SemanticCacheServicer(use_faiss=True)
    routing_processor_servicer = RoutingProcessorServicer()

    pb_grpc.add_SemanticCacheServiceServicer_to_server(semantic_cache_servicer, server)
    pb_grpc.add_RoutingProcessorServicer_to_server(routing_processor_servicer, server)

    server.add_insecure_port('[::]:50052')
    logger.info("Starting server on [::]:50052")
    await server.start()
    logger.info("Server started successfully")
    await server.wait_for_termination()

if __name__ == '__main__':
    import asyncio
    asyncio.run(serve()) 