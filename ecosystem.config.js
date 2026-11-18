module.exports = {
    apps: [
        // Long-running LLM proxy routing Claude/Codex requests to providers, registered paused.
        {
            name: "LLM Proxy - Always",
            script: "proxy",
            namespace: "Agent",
            instances: 1,
            optional: true
        }
    ]
};
