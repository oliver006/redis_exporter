local result = {}

local function lengthOfList (key)
  return redis.call("LLEN", key)
end

redis.call("SELECT", DB_NO)

local keysPresent = redis.call("KEYS", "KEYS_PATTERN")

if keysPresent ~= nil then
    for _,key in ipairs(keysPresent) do

        --error catching and status=true for success calls
     	local status, retval = pcall(lengthOfList, key)

     	if status == true then
            local keyName = "redis_list_length_" .. key
            local keyValue = retval .. ""
    		table.insert(result, keyName) -- store the keyname
         	table.insert(result, keyValue) --store the bit count
		end
    end
end

return result
