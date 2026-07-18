module.exports = {
    apps: [
        // LLM Proxy (Service)
        {
            name: "LLM Proxy",
            script: "proxy",
            namespace: "Service",
            instances: 1,
        }
    ],
};
