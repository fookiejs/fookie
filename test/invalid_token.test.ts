import * as lodash from "lodash"
import { it, describe, assert } from "vitest"
import { model, run, models, lifecycle } from "../packages/core"
import { Store, database } from "../packages/database"
import { Model, Field } from "../packages/decorator"
import { Create, Read, Count, Delete, Test, Update } from "../packages/method"
import { Text, Number, Array, Boolean, Buffer, Char, Function, Plain } from "../packages/type"
import { mixin, After, Before } from "../packages/mixin"
import { nobody, everybody, system } from "../packages/role"

it("async effect", async function () {
    @Model({ database: Store })
    class InvalidTokenModel {
        @Field({ type: Text, required: true })
        name: string

        @Field({ type: Text, required: true })
        password: string
    }
    const res = await run({
        token: 1,
        model: InvalidTokenModel,
        method: Read,
    })

    assert.equal(res.status, false)
    assert.equal(res.error, "validate_payload")
})
