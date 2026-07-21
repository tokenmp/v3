import { readFile } from "node:fs/promises";
import path from "node:path";
import { describe, it, before } from "node:test";
import assert from "node:assert/strict";
import YAML from "yaml";
import { openapiRoot } from "../scripts/contract-helpers.mjs";

const yamlPath = path.join(openapiRoot, "executor", "v1.yaml");
const openAIResponseByStatus = {
  400: "OpenAIBadRequest",
  401: "OpenAIUnauthorized",
  403: "OpenAIPermissionDenied",
  404: "OpenAINotFound",
  429: "OpenAIRateLimit",
  500: "OpenAIServerError",
  501: "OpenAINotImplemented",
  502: "OpenAIUpstreamError",
};
const anthropicResponseByStatus = {
  400: "AnthropicBadRequest",
  401: "AnthropicUnauthorized",
  403: "AnthropicPermissionDenied",
  404: "AnthropicNotFound",
  429: "AnthropicRateLimit",
  500: "AnthropicServerError",
  501: "AnthropicNotImplemented",
  529: "AnthropicOverloaded",
};
const effortLevels = ["none", "minimal", "low", "medium", "high", "xhigh", "max"];

/** Walk every mapping and reject `required: true|false` inside a schema property. */
function findPropertyLevelBooleanRequired(value, pathParts = []) {
  if (Array.isArray(value)) {
    return value.flatMap((item, index) => findPropertyLevelBooleanRequired(item, [...pathParts, String(index)]));
  }
  if (value === null || typeof value !== "object") return [];

  const violations = [];
  if (value.properties && typeof value.properties === "object" && !Array.isArray(value.properties)) {
    for (const [name, property] of Object.entries(value.properties)) {
      if (property && typeof property === "object" && typeof property.required === "boolean") {
        violations.push([...pathParts, "properties", name, "required"].join("."));
      }
    }
  }
  for (const [key, child] of Object.entries(value)) {
    violations.push(...findPropertyLevelBooleanRequired(child, [...pathParts, key]));
  }
  return violations;
}

function assertErrorResponses(operation, expectedByStatus, protocol) {
  const actualStatuses = Object.keys(operation.responses)
    .filter((status) => status !== "200")
    .sort();
  assert.deepStrictEqual(actualStatuses, Object.keys(expectedByStatus).sort());

  for (const [status, component] of Object.entries(expectedByStatus)) {
    assert.equal(
      operation.responses[status].$ref,
      `#/components/responses/${component}`,
      `${protocol} ${status} must use ${component}`,
    );
  }
}

describe("Executor v1 OpenAPI contract", () => {
  let doc;

  before(async () => {
    doc = YAML.parse(await readFile(yamlPath, "utf8"), { strict: true });
  });

  it("declares the confirmed design status without claiming unimplemented capabilities", () => {
    const description = doc.info.description;
    for (const fragment of [
      "confirmed design contract",
      "Only the `/healthz` Foundation endpoint is currently implemented",
      "not yet implemented",
      "does not claim SDK integration or",
      "upstream forwarding is available today",
    ]) {
      assert.ok(description.includes(fragment), `info.description must include '${fragment}'`);
    }
  });

  it("defines every Executor endpoint with its expected HTTP operation", () => {
    const expectedOperations = {
      "/healthz": ["get", "head"],
      "/v1/models": ["get"],
      "/v1/chat/completions": ["post"],
      "/v1/messages": ["post"],
      "/v1/responses": ["post"],
      "/v1/images/generations": ["post"],
    };

    assert.deepStrictEqual(Object.keys(doc.paths).sort(), Object.keys(expectedOperations).sort());
    for (const [endpoint, methods] of Object.entries(expectedOperations)) {
      assert.deepStrictEqual(Object.keys(doc.paths[endpoint]).sort(), methods, `Unexpected operations for '${endpoint}'`);
      for (const method of methods) {
        assert.equal(typeof doc.paths[endpoint][method].operationId, "string", `${method.toUpperCase()} ${endpoint} needs operationId`);
      }
    }
  });

  it("binds every protocol error status to its native response component", () => {
    const openAIEndpoints = [
      ["/v1/models", "get", { 401: "OpenAIUnauthorized", 500: "OpenAIServerError", 501: "OpenAINotImplemented" }],
      ["/v1/chat/completions", "post", openAIResponseByStatus],
      ["/v1/responses", "post", openAIResponseByStatus],
      ["/v1/images/generations", "post", { 400: "OpenAIBadRequest", 401: "OpenAIUnauthorized", 404: "OpenAINotFound", 429: "OpenAIRateLimit", 500: "OpenAIServerError", 501: "OpenAINotImplemented", 502: "OpenAIUpstreamError" }],
    ];
    for (const [endpoint, method, responses] of openAIEndpoints) {
      assertErrorResponses(doc.paths[endpoint][method], responses, "OpenAI");
    }
    assertErrorResponses(doc.paths["/v1/messages"].post, anthropicResponseByStatus, "Anthropic");
  });

  it("uses protocol-native error schemas and OpenAI example statuses matching their HTTP components", () => {
    const { schemas, responses } = doc.components;
    assert.deepStrictEqual(schemas.OpenAIErrorResponse.required, ["error"]);
    assert.deepStrictEqual(schemas.OpenAIErrorResponse.properties.error.required, ["message", "type"]);
    assert.equal(schemas.OpenAIErrorResponse.properties.status.type, "integer");
    assert.deepStrictEqual(schemas.AnthropicErrorResponse.required, ["type", "error"]);
    assert.deepStrictEqual(schemas.AnthropicErrorResponse.properties.type.enum, ["error"]);
    assert.deepStrictEqual(schemas.AnthropicErrorResponse.properties.error.required, ["type", "message"]);

    for (const [status, component] of Object.entries(openAIResponseByStatus)) {
      const json = responses[component].content["application/json"];
      assert.equal(json.schema.$ref, "#/components/schemas/OpenAIErrorResponse");
      assert.equal(json.example.status, Number(status), `${component} example status must match ${status}`);
    }
    for (const component of Object.values(anthropicResponseByStatus)) {
      assert.equal(responses[component].content["application/json"].schema.$ref, "#/components/schemas/AnthropicErrorResponse");
    }

    const openAINotImplemented = responses.OpenAINotImplemented.content["application/json"].example;
    assert.deepStrictEqual(openAINotImplemented, {
      error: {
        message: "This endpoint is not implemented.",
        type: "api_error",
        code: "NOT_IMPLEMENTED",
        param: null,
      },
      status: 501,
    });
    const anthropicNotImplemented = responses.AnthropicNotImplemented.content["application/json"].example;
    assert.deepStrictEqual(anthropicNotImplemented, {
      type: "error",
      error: {
        type: "api_error",
        message: "This endpoint is not implemented.",
      },
      request_id: "req_abc123",
    });
  });

  it("documents SSE framing and each protocol's terminal event semantics", () => {
    const streamDescriptions = {
      "/v1/chat/completions": ["Server-Sent Events", "data: [DONE]"],
      "/v1/messages": ["Server-Sent Events", "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop", "ping"],
      "/v1/responses": ["Server-Sent Events", "response.completed", "response.failed"],
    };
    for (const [endpoint, expectedFragments] of Object.entries(streamDescriptions)) {
      const sse = doc.paths[endpoint].post.responses["200"].content["text/event-stream"];
      assert.equal(sse.schema.type, "string");
      for (const fragment of expectedFragments) {
        assert.ok(sse.schema.description.includes(fragment), `${endpoint} SSE description must include '${fragment}'`);
      }
    }
    assert.equal(doc.paths["/v1/images/generations"].post.responses["200"].content["text/event-stream"], undefined);
  });

  it("defaults all streaming requests to false", () => {
    const schemas = doc.components.schemas;
    for (const name of ["CreateChatCompletionRequest", "CreateMessageRequest", "CreateResponseRequest"]) {
      assert.equal(schemas[name].properties.stream.type, "boolean");
      assert.equal(schemas[name].properties.stream.default, false, `${name}.stream must default to false`);
    }
  });

  it("models complete thinking capability and protocol-specific thinking controls", () => {
    const schemas = doc.components.schemas;
    const modelThinking = schemas.ModelThinkingConfig;
    assert.ok(schemas.ModelObject.properties.capabilities.items.enum.includes("thinking"));
    assert.equal(schemas.ModelObject.properties.thinking.$ref, "#/components/schemas/ModelThinkingConfig");
    assert.equal(schemas.ModelObject.properties.thinking.nullable, true);
    assert.deepStrictEqual(modelThinking.required, ["supported", "default_effort", "max_effort", "effort_levels"]);
    assert.equal(modelThinking.properties.supported.type, "boolean");
    assert.deepStrictEqual(modelThinking.properties.default_effort.enum, effortLevels);
    assert.deepStrictEqual(modelThinking.properties.max_effort.enum, effortLevels);
    assert.deepStrictEqual(modelThinking.properties.effort_levels.items.enum, effortLevels);
    assert.equal(modelThinking.properties.min_budget_tokens.type, "integer");
    assert.equal(modelThinking.properties.max_budget_tokens.type, "integer");

    assert.deepStrictEqual(schemas.ThinkingConfig.required, ["type"]);
    assert.deepStrictEqual(schemas.ThinkingConfig.properties.type.enum, ["enabled", "disabled"]);
    assert.equal(schemas.ThinkingConfig.properties.budget_tokens.minimum, 1024);
    assert.deepStrictEqual(schemas.ThinkingConfig.properties.display.enum, ["summarized", "omitted"]);
    assert.deepStrictEqual(schemas.ResponseReasoningConfig.properties.effort.enum, effortLevels);
    assert.deepStrictEqual(schemas.ResponseReasoningConfig.properties.summary.enum, ["auto", "detailed", "none"]);
    assert.equal(schemas.AnthropicContentBlock.properties.signature.type, "string");
    assert.equal(schemas.CreateMessageRequest.properties.thinking.$ref, "#/components/schemas/ThinkingConfig");
    assert.equal(schemas.CreateResponseRequest.properties.reasoning.$ref, "#/components/schemas/ResponseReasoningConfig");
    assert.deepStrictEqual(schemas.CreateChatCompletionRequest.properties.reasoning_effort.enum, effortLevels);
  });

  it("declares exact required fields on each Create request and defines every required property", () => {
    const expectedRequired = {
      CreateChatCompletionRequest: ["model", "messages"],
      CreateMessageRequest: ["model", "messages", "max_tokens"],
      CreateResponseRequest: ["model", "input"],
      CreateImageRequest: ["model", "prompt"],
    };
    for (const [name, required] of Object.entries(expectedRequired)) {
      const schema = doc.components.schemas[name];
      assert.deepStrictEqual(schema.required, required, `${name}.required must be exact`);
      for (const property of required) {
        assert.ok(schema.properties[property], `${name}.required references missing property '${property}'`);
      }
    }
    assert.deepStrictEqual(findPropertyLevelBooleanRequired(doc), []);
  });

  it("closes finite request objects with additionalProperties:false while leaving arbitrary user JSON/JSON Schema open", () => {
    const schemas = doc.components.schemas;

    // Finite request objects and their nested finite schemas must be closed.
    // The OpenAI Chat message/content-part/tool-call request schemas are
    // separated from their response-side twins: request schemas are closed
    // (ChatRequest*), while the response-side ChatMessage/ChatContentPart/
    // ChatToolCall stay open so success responses carry provider extensions.
    const closedSchemas = [
      "CreateChatCompletionRequest",
      "ChatRequestMessage",
      "ChatRequestContentPart",
      "ChatRequestToolCall",
      "ChatTool",
      "ChatToolChoice",
      "CreateMessageRequest",
      "ThinkingConfig",
      "AnthropicSystemBlock",
      "AnthropicMessage",
      "AnthropicContentBlock",
      "AnthropicToolChoice",
      "CreateResponseRequest",
      "ResponseInputItem",
      "ResponseContentPart",
      "ResponseTool",
      "ResponseReasoningConfig",
      "CreateImageRequest",
    ];
    for (const name of closedSchemas) {
      assert.equal(
        schemas[name].additionalProperties,
        false,
        `${name} must declare additionalProperties:false`,
      );
    }

    // Nested finite objects inside pure-request trees must also be closed,
    // including the OpenAI Chat request image_url and tool-call function.
    assert.equal(schemas.ChatTool.properties.function.additionalProperties, false);
    assert.equal(schemas.ChatToolChoice.properties.function.additionalProperties, false);
    assert.equal(schemas.ChatRequestContentPart.properties.image_url.additionalProperties, false);
    assert.equal(schemas.ChatRequestToolCall.properties.function.additionalProperties, false);
    assert.equal(schemas.CreateChatCompletionRequest.properties.response_format.additionalProperties, false);
    assert.equal(schemas.AnthropicContentBlock.properties.source.additionalProperties, false);
    assert.equal(schemas.AnthropicContentBlock.properties.cache_control.additionalProperties, false);
    assert.equal(schemas.AnthropicSystemBlock.properties.cache_control.additionalProperties, false);
    assert.equal(schemas.AnthropicTool.properties.cache_control.additionalProperties, false);
    assert.equal(schemas.CreateMessageRequest.properties.metadata.additionalProperties, false);
    assert.equal(schemas.CreateResponseRequest.properties.text.additionalProperties, false);
    assert.equal(schemas.CreateResponseRequest.properties.text.properties.format.additionalProperties, false);
    assert.equal(schemas.CreateResponseRequest.properties.metadata.additionalProperties, false);

    // The request references the closed ChatRequestMessage twin...
    assert.equal(
      schemas.CreateChatCompletionRequest.properties.messages.items.$ref,
      "#/components/schemas/ChatRequestMessage",
      "CreateChatCompletionRequest.messages must reference the closed ChatRequestMessage",
    );
    // ...while the success response references the open ChatMessage twin.
    assert.equal(
      schemas.ChatCompletionChoice.properties.message.$ref,
      "#/components/schemas/ChatMessage",
      "ChatCompletionChoice.message must reference the open ChatMessage",
    );

    // Response-side Chat message/content-part/tool-call schemas must stay
    // open (extensible) so provider extensions still contract-validate.
    const responseOpenSchemas = ["ChatMessage", "ChatContentPart", "ChatToolCall"];
    for (const name of responseOpenSchemas) {
      assert.notEqual(
        schemas[name].additionalProperties,
        false,
        `${name} is a response-side schema and must stay open`,
      );
    }

    // Arbitrary user JSON / JSON Schema must remain open (no additionalProperties key).
    const openSchemasByPointer = [
      [schemas.ChatTool.properties.function, "ChatTool.function.parameters"],
      [schemas.AnthropicContentBlock, "AnthropicContentBlock.input"],
      [schemas.AnthropicTool, "AnthropicTool.input_schema"],
      [schemas.AnthropicResponseContentBlock, "AnthropicResponseContentBlock.input"],
      [schemas.ResponseTool, "ResponseTool.parameters"],
      [schemas.CreateResponseRequest.properties.text.properties.format, "CreateResponseRequest.text.format.schema"],
    ];
    for (const [obj, label] of openSchemasByPointer) {
      const prop = obj.properties.parameters || obj.properties.input || obj.properties.input_schema || obj.properties.schema;
      assert.ok(prop, `${label} property must exist`);
      assert.equal(
        prop.additionalProperties,
        undefined,
        `${label} must remain open (no additionalProperties constraint)`,
      );
    }
  });

  it("does not close success response schemas (responses stay extensible)", () => {
    const schemas = doc.components.schemas;
    const responseSchemas = [
      "ChatCompletionResponse",
      "ChatCompletionChoice",
      "ChatUsage",
      "CreateMessageResponse",
      "AnthropicResponseContentBlock",
      "AnthropicUsage",
      "ResponseResponse",
      "ResponseOutputItem",
      "ResponseOutputContent",
      "ResponseUsage",
      "CreateImageResponse",
      "ModelListResponse",
      "ModelObject",
      "ModelThinkingConfig",
      "HealthResponse",
      "OpenAIErrorResponse",
      "AnthropicErrorResponse",
      // OpenAI Chat response-side twins (request twins are closed above).
      "ChatMessage",
      "ChatContentPart",
      "ChatToolCall",
    ];
    for (const name of responseSchemas) {
      assert.notEqual(
        schemas[name].additionalProperties,
        false,
        `${name} is a response/error schema and must not be closed`,
      );
    }
  });

  it("documents the observable Anthropic thinking budget/max conditional rule and source/image safety boundaries", () => {
    const description = doc.info.description;
    assert.ok(
      description.includes("1024 <= budget_tokens < max_tokens"),
      "info.description must document the thinking budget/max conditional rule",
    );
    assert.ok(
      description.includes("defensively enforces these observable bounds"),
      "info.description must document defensive enforcement",
    );

    const source = doc.components.schemas.AnthropicContentBlock.properties.source;
    assert.deepStrictEqual(source.properties.media_type.enum, ["image/jpeg", "image/png", "image/gif", "image/webp"]);
    assert.ok(source.properties.data.description.toLowerCase().includes("base64"), "source.data must describe base64 encoding");
    assert.ok(source.properties.data.description.includes("rejected"), "source.data must describe rejection of unsafe encodings");

    const openAIImageUrl = doc.components.schemas.ChatRequestContentPart.properties.image_url.properties.url;
    assert.ok(openAIImageUrl.description.includes("https"), "ChatRequestContentPart.image_url.url must describe the HTTPS policy");
    assert.ok(openAIImageUrl.description.includes("data:"), "ChatRequestContentPart.image_url.url must describe the data: URL policy");
    assert.ok(openAIImageUrl.description.includes("rejected"), "ChatRequestContentPart.image_url.url must describe rejection");

    const responseImageUrl = doc.components.schemas.ResponseContentPart.properties.image_url;
    assert.ok(responseImageUrl.description.includes("https"), "ResponseContentPart.image_url must describe the HTTPS policy");
    assert.ok(responseImageUrl.description.includes("data:"), "ResponseContentPart.image_url must describe the data: URL policy");
  });
});
