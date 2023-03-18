import { it, describe, assert } from "vitest"
import { model, run, models } from "../src"
import { Store } from "../src/databases"
import { Model, Field } from "../src/decorators"
import { Create, Read } from "../src/methods"
import { Text, Number } from "../src/types"
import * as lodash from "lodash"

it("async effect", async function () {
    const res = await run({
        model: "not_existed_model_1",
        method: "read",
    })

    assert.equal(res.status, false)
})
