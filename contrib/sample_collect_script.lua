-- Example collect script for -script option
-- This returns a Lua table with alternating keys and values.
-- Both keys and values must be strings, similar to a HGETALL result.
-- More info about Redis Lua scripting: https://redis.io/commands/eval

local result = {}

-- Add all keys and values from some hash in db 5
redis.call("SELECT", 5)
local r = redis.call("HGETALL", "some-hash-with-stats")
if r ~= nil then
    for _,v in ipairs(r) do
        table.insert(result, v) -- alternating keys and values
    end
end

-- Set foo to 42
table.insert(result, "foo")
table.insert(result, "42") -- note the string, use tostring() if needed

return result
